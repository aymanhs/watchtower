// Package storage is Watchtower's own built-in time-series storage —
// deliberately much smaller than NanoTDB (the project this was learned
// from, not copied from): no WAL, no compression, no multi-database
// namespacing, no precomputed rollups. At this project's actual scale (a
// handful of servers, a few dozen metrics, 5s-60s intervals) those exist
// to solve problems — flash-wear on a Pi writing thousands of samples a
// second, query latency over millions of points — that don't apply here.
// What's kept, because it earns its place at any scale: one append-only
// file per day (retention is just deleting old files, no compaction), and
// a small catalog mapping metric names to compact IDs.
//
// Durability is deliberately periodic, not per-write: every Append's
// Write lands in the OS page cache immediately (so a same-process Query
// right after an Append always sees it — fsync only matters for
// surviving a crash, not for read-your-own-writes within one process),
// and an explicit fsync happens on whichever comes first — syncEvery
// writes, or syncInterval of wall-clock time — plus always on Close and
// on a day's partition rolling over. At this project's actual write rate
// this still bounds the worst case (an unclean kill, not a graceful
// shutdown) to at most syncEvery writes or syncInterval of data, not "the
// whole day since the process started" and not "every write syncs",
// which turned out to be more fsync-ing than this scale needs.
package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// recordSize is one point on disk: a 2-byte metric ID, an 8-byte Unix-nano
// timestamp, an 8-byte float64 value — fixed width, no framing needed.
const recordSize = 2 + 8 + 8

const dateLayout = "2006-01-02"

// defaultSyncInterval/defaultSyncEvery are the two thresholds that
// trigger a periodic fsync — whichever is reached first. Chosen for this
// project's actual write rate (collection cycles measured in seconds,
// combined well under one write/sec across every metric on every
// server): either bound alone would already be a small worst-case loss
// window on an unclean kill; together they cover both "nothing was
// written for a while, sync anyway" and "a burst of writes happened
// quickly, don't wait the full interval".
const (
	defaultSyncInterval = 15 * time.Minute
	defaultSyncEvery    = 50
)

// Point is one stored (timestamp, value) pair, returned by Query.
type Point struct {
	Timestamp time.Time
	Value     float64
}

// Store is Watchtower's time-series store: one directory, one catalog,
// one append-only file per UTC day. Safe for concurrent use.
type Store struct {
	dir          string
	retention    time.Duration // <=0 means ApplyRetention never deletes anything
	syncInterval time.Duration
	syncEvery    int
	catalog      *catalog

	// now is time.Now, overridden only by tests that need to control
	// when the interval threshold trips without actually sleeping.
	now func() time.Time

	mu            sync.Mutex
	openDate      string
	openFile      *os.File
	pendingWrites int
	lastSync      time.Time
}

// Open creates dir if needed and loads (or starts) the catalog in it.
// retention <= 0 disables automatic deletion — ApplyRetention becomes a
// no-op, files just accumulate until retention is actually set.
func Open(dir string, retention time.Duration) (*Store, error) {
	if err := ensureDir(dir); err != nil {
		return nil, fmt.Errorf("create storage dir %s: %w", dir, err)
	}
	cat, err := loadCatalog(filepath.Join(dir, "catalog.json"))
	if err != nil {
		return nil, err
	}
	return &Store{
		dir: dir, retention: retention, catalog: cat,
		syncInterval: defaultSyncInterval, syncEvery: defaultSyncEvery,
		now: time.Now, lastSync: time.Now(),
	}, nil
}

// Close releases the currently-open partition file, if any — syncing it
// first (see closeOpenFileLocked), so a graceful shutdown never loses
// whatever's been written since the last periodic sync.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeOpenFileLocked()
}

// closeOpenFileLocked syncs and closes whatever file is currently open —
// called both by Close (process shutting down) and by fileForLocked
// rolling a day's partition over to the next one. Close(2) alone does
// not guarantee the OS page cache was flushed to disk; only an explicit
// Sync does, so one always happens here before the handle goes away,
// regardless of where the periodic write-count/interval thresholds
// happen to be.
func (s *Store) closeOpenFileLocked() error {
	if s.openFile == nil {
		return nil
	}
	syncErr := s.openFile.Sync()
	closeErr := s.openFile.Close()
	s.openFile, s.openDate = nil, ""
	s.pendingWrites = 0
	s.lastSync = s.now()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Append records one point for name at ts, creating a catalog entry for
// name if this is its first time. The write always lands in the file
// immediately — a Query in this same process sees it right away
// regardless of fsync, since fsync only affects surviving a crash, not
// read-your-own-writes — but it's only made durable against a crash
// periodically: whichever comes first of syncEvery writes or
// syncInterval of wall-clock time since the last sync (see the package
// doc comment for why per-write fsync turned out to be more than this
// project's scale needs). Close and a day's partition rolling over
// always sync regardless of these thresholds (closeOpenFileLocked).
func (s *Store) Append(name string, ts time.Time, value float64) error {
	id, err := s.catalog.idFor(name)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	var rec [recordSize]byte
	binary.LittleEndian.PutUint16(rec[0:2], id)
	binary.LittleEndian.PutUint64(rec[2:10], uint64(ts.UnixNano()))
	binary.LittleEndian.PutUint64(rec[10:18], math.Float64bits(value))

	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.fileForLocked(ts.UTC().Format(dateLayout))
	if err != nil {
		return fmt.Errorf("storage: open partition for %s: %w", name, err)
	}
	if _, err := f.Write(rec[:]); err != nil {
		return fmt.Errorf("storage: write %s: %w", name, err)
	}
	s.pendingWrites++
	if s.pendingWrites >= s.syncEvery || s.now().Sub(s.lastSync) >= s.syncInterval {
		if err := f.Sync(); err != nil {
			return fmt.Errorf("storage: sync %s: %w", name, err)
		}
		s.pendingWrites = 0
		s.lastSync = s.now()
	}
	return nil
}

// fileForLocked returns the append-mode handle for date's partition,
// opening it (and closing whatever was open for a different date) if
// needed. Caller must hold s.mu. In steady state this only actually
// opens a new file once a day, at UTC midnight.
func (s *Store) fileForLocked(date string) (*os.File, error) {
	if s.openFile != nil && s.openDate == date {
		return s.openFile, nil
	}
	if err := s.closeOpenFileLocked(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.partitionPath(date), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.openFile, s.openDate = f, date
	return f, nil
}

func (s *Store) partitionPath(date string) string {
	return filepath.Join(s.dir, "data-"+date+".dat")
}

// Query returns name's points with a timestamp in [from, to], oldest
// first. A name that was never recorded returns (nil, nil) — that's "no
// data", not an error.
func (s *Store) Query(name string, from, to time.Time) ([]Point, error) {
	id, ok := s.catalog.lookupID(name)
	if !ok {
		return nil, nil
	}

	var points []Point
	for _, date := range datesBetween(from, to) {
		data, err := os.ReadFile(s.partitionPath(date))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("storage: read partition %s: %w", date, err)
		}
		for off := 0; off+recordSize <= len(data); off += recordSize {
			gotID := binary.LittleEndian.Uint16(data[off : off+2])
			if gotID != id {
				continue
			}
			ns := int64(binary.LittleEndian.Uint64(data[off+2 : off+10]))
			ts := time.Unix(0, ns)
			if ts.Before(from) || ts.After(to) {
				continue
			}
			v := math.Float64frombits(binary.LittleEndian.Uint64(data[off+10 : off+18]))
			points = append(points, Point{Timestamp: ts, Value: v})
		}
	}
	// Each partition's own extracted points already arrive in order (one
	// metric's observations are appended in the order they were
	// collected), and partitions are walked oldest-first, so this is a
	// no-op in the normal case — a cheap defensive net against clock skew
	// or a restart that briefly backdated something, not load-bearing.
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	return points, nil
}

// datesBetween returns every UTC calendar date (YYYY-MM-DD) that [from,
// to] touches, inclusive, in order.
func datesBetween(from, to time.Time) []string {
	if to.Before(from) {
		return nil
	}
	from, to = from.UTC(), to.UTC()
	var dates []string
	for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC); !d.After(to); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format(dateLayout))
	}
	return dates
}

// ApplyRetention deletes partition files entirely older than the Store's
// retention window. A no-op if retention is <= 0. Meant to be called
// periodically by the caller (e.g. once at startup and once a day) —
// Store itself runs no background goroutine of its own.
func (s *Store) ApplyRetention() error {
	if s.retention <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().Add(-s.retention)
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("storage: list %s: %w", s.dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "data-") || !strings.HasSuffix(name, ".dat") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, "data-"), ".dat")
		date, err := time.Parse(dateLayout, dateStr)
		if err != nil {
			continue // not a partition file we recognize; leave it alone
		}
		if date.Before(cutoff) {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
				return fmt.Errorf("storage: remove expired partition %s: %w", name, err)
			}
		}
	}
	return nil
}
