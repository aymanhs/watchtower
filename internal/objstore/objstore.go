// Package objstore keeps Watchtower's objects: named JSON values (a
// container list, top processes, ...) that don't fit a single number.
// The latest value per name lives in memory; every change is appended as
// one line to a per-day file beside the metric history, so cleanup is
// just deleting old files.
package objstore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	filePrefix = "objects-"
	fileSuffix = ".jsonl"
	dateLayout = "2006-01-02"
)

// Entry is one object value at a point in time.
type Entry struct {
	Name  string          `json:"name"`
	Time  time.Time       `json:"ts"`
	Value json.RawMessage `json:"v"`
}

// Store holds the latest Entry per name and the on-disk change log.
type Store struct {
	dir       string
	retention time.Duration

	mu      sync.Mutex
	latest  map[string]Entry
	written map[string]json.RawMessage // last value persisted, to skip unchanged ones
}

// Open creates dir if needed and restores the latest value of every name
// from the existing day files.
func Open(dir string, retention time.Duration) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("objstore: create %s: %w", dir, err)
	}
	s := &Store{dir: dir, retention: retention, latest: map[string]Entry{}, written: map[string]json.RawMessage{}}
	files, err := s.files()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		_ = scan(f.path, func(e Entry) {
			s.latest[e.Name] = e
			s.written[e.Name] = e.Value
		})
	}
	return s, nil
}

// Put records value (compact JSON) as name's latest. It is appended to the
// day file only if it differs from the last persisted value; changed
// reports whether it was.
func (s *Store) Put(name string, ts time.Time, value json.RawMessage) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest[name] = Entry{Name: name, Time: ts, Value: value}
	if bytes.Equal(s.written[name], value) {
		return false, nil
	}
	line, err := json.Marshal(Entry{Name: name, Time: ts.UTC(), Value: value})
	if err != nil {
		return false, err
	}
	path := filepath.Join(s.dir, filePrefix+ts.UTC().Format(dateLayout)+fileSuffix)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("objstore: %w", err)
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return false, fmt.Errorf("objstore: %w", werr)
	}
	s.written[name] = value
	return true, nil
}

// Latest returns name's most recent value.
func (s *Store) Latest(name string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.latest[name]
	return e, ok
}

// Snapshot returns every name's latest entry, sorted by name.
func (s *Store) Snapshot() []Entry {
	s.mu.Lock()
	out := make([]Entry, 0, len(s.latest))
	for _, e := range s.latest {
		out = append(out, e)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// History returns name's stored changes within [from, to], oldest first.
func (s *Store) History(name string, from, to time.Time) ([]Entry, error) {
	files, err := s.files()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, f := range files {
		if f.date.Add(24*time.Hour).Before(from) || f.date.After(to) {
			continue
		}
		_ = scan(f.path, func(e Entry) {
			if e.Name == name && !e.Time.Before(from) && !e.Time.After(to) {
				out = append(out, e)
			}
		})
	}
	return out, nil
}

// ApplyRetention deletes day files entirely older than the retention
// window. A no-op if retention <= 0.
func (s *Store) ApplyRetention() error {
	if s.retention <= 0 {
		return nil
	}
	files, err := s.files()
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-s.retention)
	for _, f := range files {
		if f.date.Before(cutoff) {
			if err := os.Remove(f.path); err != nil {
				return fmt.Errorf("objstore: remove %s: %w", f.path, err)
			}
		}
	}
	return nil
}

type dayFile struct {
	path string
	date time.Time
}

// files lists the day files, oldest first.
func (s *Store) files() ([]dayFile, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("objstore: list %s: %w", s.dir, err)
	}
	var out []dayFile
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, filePrefix) || !strings.HasSuffix(n, fileSuffix) {
			continue
		}
		d, err := time.Parse(dateLayout, strings.TrimSuffix(strings.TrimPrefix(n, filePrefix), fileSuffix))
		if err != nil {
			continue
		}
		out = append(out, dayFile{filepath.Join(s.dir, n), d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out, nil
}

// scan calls fn for every well-formed line of path; malformed lines (a
// torn write) are skipped.
func scan(path string, fn func(Entry)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Name != "" {
			fn(e)
		}
	}
	return sc.Err()
}
