package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// catalog maps a metric's full name (e.g. "jumpbox/cpu.usage") to the
// small uint16 ID that's actually written into data files — 2 bytes
// instead of a variable-length string on every single record. Persisted
// as plain JSON (name -> ID): small, human-readable, and trivial to
// inspect without any tooling, same spirit as this project's YAML config.
type catalog struct {
	mu     sync.Mutex
	path   string
	byName map[string]uint16
	byID   map[uint16]string
	next   uint16
}

func loadCatalog(path string) (*catalog, error) {
	c := &catalog{path: path, byName: make(map[string]uint16), byID: make(map[uint16]string), next: 1}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil // first run: empty catalog, created on first idFor
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c.byName); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	for name, id := range c.byName {
		c.byID[id] = name
		if id >= c.next {
			c.next = id + 1
		}
	}
	return c, nil
}

// idFor returns name's ID, assigning and persisting a new one if name
// hasn't been seen before. Persisting is write-to-temp-then-rename — the
// catalog only changes when a genuinely new metric name shows up (rare
// after the first few collection cycles), so this isn't a hot path.
func (c *catalog) idFor(name string) (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.byName[name]; ok {
		return id, nil
	}
	if c.next == 0 {
		return 0, fmt.Errorf("catalog full: no more metric IDs available (uint16 exhausted)")
	}
	id := c.next
	c.next++
	c.byName[name] = id
	c.byID[id] = name
	if err := c.save(); err != nil {
		// Roll back: a name that didn't persist must not be handed out,
		// or a restart would silently lose track of what this ID means.
		delete(c.byName, name)
		delete(c.byID, id)
		c.next--
		return 0, err
	}
	return id, nil
}

// lookupID returns name's ID without creating one — for queries, where a
// never-recorded metric should just mean "no data", not a new catalog entry.
func (c *catalog) lookupID(name string) (uint16, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.byName[name]
	return id, ok
}

func (c *catalog) nameFor(id uint16) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name, ok := c.byID[id]
	return name, ok
}

// save must be called with c.mu held.
func (c *catalog) save() error {
	data, err := json.MarshalIndent(c.byName, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write catalog temp file: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("rename catalog into place: %w", err)
	}
	return nil
}

// ensureDir is a tiny helper Open uses so callers don't need their own
// os.MkdirAll before pointing a Store at a fresh directory.
func ensureDir(dir string) error {
	return os.MkdirAll(filepath.Clean(dir), 0o755)
}
