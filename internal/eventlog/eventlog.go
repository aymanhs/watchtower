// Package eventlog persists event.Event transitions as append-only JSONL,
// one file per UTC day (events-YYYY-MM-DD.jsonl), so outage history
// survives restarts. It is deliberately dumb: no index, no rewriting;
// retention just deletes old day files, like internal/storage does for
// metric points. The event engine itself stays in memory and file-free.
package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aymanhs/watchtower/internal/event"
)

const (
	filePrefix = "events-"
	fileSuffix = ".jsonl"
	dateLayout = "2006-01-02"
)

// Log appends events to day files under a directory.
type Log struct {
	dir       string
	retention time.Duration // <=0 keeps everything

	mu   sync.Mutex
	date string
	f    *os.File
}

// Open creates dir if needed. retention <= 0 disables ApplyRetention.
func Open(dir string, retention time.Duration) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: create %s: %w", dir, err)
	}
	return &Log{dir: dir, retention: retention}, nil
}

// Append writes one event as a JSON line, handed to the OS immediately
// (events are rare, so there is no batching).
func (l *Log) Append(ev event.Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("eventlog: encode event %d: %w", ev.ID, err)
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	date := ev.Timestamp.UTC().Format(dateLayout)
	if l.f == nil || l.date != date {
		if l.f != nil {
			_ = l.f.Close()
			l.f = nil
		}
		f, err := os.OpenFile(l.path(date), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("eventlog: open day file: %w", err)
		}
		l.f, l.date = f, date
	}
	if _, err := l.f.Write(line); err != nil {
		return fmt.Errorf("eventlog: write: %w", err)
	}
	return nil
}

// Load returns every persisted event with Timestamp >= since, oldest
// first. Unparseable lines (e.g. a torn final write after a crash) are
// skipped rather than failing the whole load.
func (l *Log) Load(since time.Time) ([]event.Event, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, fmt.Errorf("eventlog: list %s: %w", l.dir, err)
	}
	var names []string
	for _, e := range entries {
		if d, ok := fileDate(e.Name()); ok && !d.AddDate(0, 0, 1).Before(since) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []event.Event
	for _, name := range names {
		f, err := os.Open(filepath.Join(l.dir, name))
		if err != nil {
			return nil, fmt.Errorf("eventlog: open %s: %w", name, err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var ev event.Event
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Timestamp.Before(since) {
				continue
			}
			out = append(out, ev)
		}
		err = sc.Err()
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("eventlog: read %s: %w", name, err)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// ApplyRetention deletes day files entirely older than the retention window.
func (l *Log) ApplyRetention() error {
	if l.retention <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().Add(-l.retention)
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("eventlog: list %s: %w", l.dir, err)
	}
	for _, e := range entries {
		if d, ok := fileDate(e.Name()); ok && d.Before(cutoff) {
			if err := os.Remove(filepath.Join(l.dir, e.Name())); err != nil {
				return fmt.Errorf("eventlog: remove %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}

// Close closes the open day file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func (l *Log) path(date string) string {
	return filepath.Join(l.dir, filePrefix+date+fileSuffix)
}

func fileDate(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return time.Time{}, false
	}
	d, err := time.Parse(dateLayout, strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix))
	return d, err == nil
}
