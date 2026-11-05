package dashboard

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// SourceFile is set by LoadStore/Store.Save, never read from YAML — it's
// the file this Dashboard round-trips through, same spirit as
// server.Config.SourceFile.
//
// It lives on Dashboard itself (not a wrapper struct) so one value is
// "the dashboard" everywhere — a GUI holds a *Dashboard and can Save it
// without also threading a separate path around.

// Store is every Dashboard loaded from one directory, plus enough to
// persist edits back — the GUI's add/remove/edit-widget controls call
// Save, same as a server's own dashboard controls used to call
// server.SaveDashboard before a dashboard lived in its own file.
type Store struct {
	dir string

	mu     sync.Mutex
	byName map[string]*Dashboard
}

// LoadStore walks dir (non-recursive is enough today, but WalkDir costs
// nothing extra) for every .yaml/.yml file, parsing each as one
// Dashboard. A missing dir is not an error — same as group.LoadGroups,
// dashboards are optional; a Store with nothing in it is a perfectly
// normal "nobody's built a dashboard yet" state. Names must be unique
// across files, same reasoning as server.LoadServers: this package keys
// everything by Dashboard.Name.
func LoadStore(dir string) (*Store, error) {
	s := &Store{dir: dir, byName: make(map[string]*Dashboard)}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		dash, err := loadOne(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if first, dup := s.byName[dash.Name]; dup {
			return fmt.Errorf("%s: duplicate dashboard name %q, also defined in %s", path, dash.Name, first.SourceFile)
		}
		s.byName[dash.Name] = dash
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	return s, nil
}

func loadOne(path string) (*Dashboard, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Dashboard
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	d.SourceFile = path
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// All returns every loaded Dashboard, in no particular order — a caller
// wanting a stable order (a picker UI, say) sorts it itself.
func (s *Store) All() []*Dashboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Dashboard, 0, len(s.byName))
	for _, d := range s.byName {
		out = append(out, d)
	}
	return out
}

// Get returns the dashboard named name, if the Store has one.
func (s *Store) Get(name string) (*Dashboard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byName[name]
	return d, ok
}

// GetOrNew returns the dashboard named name if the Store already has one,
// else a brand new, not-yet-saved one — convenient for a caller that
// always wants a *Dashboard to append a widget to regardless of whether
// anyone's created it yet (e.g. "add this server's first widget").
func (s *Store) GetOrNew(name string) *Dashboard {
	if d, ok := s.Get(name); ok {
		return d
	}
	return &Dashboard{Name: name}
}

// slugRE matches anything that isn't a lowercase letter, digit or
// hyphen — everything else becomes a hyphen when turning a Dashboard's
// Name into a filename.
var slugRE = regexp.MustCompile(`[^a-z0-9-]+`)

// slug turns name into a filesystem-safe, human-recognizable basename —
// "My Dashboard!" becomes "my-dashboard-", trimmed to "my-dashboard".
func slug(name string) string {
	s := slugRE.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "dashboard"
	}
	return s
}

// Save validates d, writes it to its own SourceFile (choosing one under
// the Store's dir, from d.Name, the first time) and records it in the
// Store under d.Name. Edits an existing file's yaml.Node tree rather
// than a wholesale yaml.Marshal, same reasoning as server.SaveDashboard
// used to: a dashboard file is still something a person can reasonably
// hand-edit and comment, and a full remarshal would discard that. A
// brand new file has no existing document to preserve anything of, so
// it's just marshaled fresh.
func (s *Store) Save(d *Dashboard) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("refusing to save an invalid dashboard: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.byName[d.Name]; ok && existing != d {
		// Renaming onto a name some other *Dashboard already owns would
		// silently merge two files into one on next load — same
		// collision server.LoadServers guards against, just caught here
		// at write time instead of at the next load.
		return fmt.Errorf("another dashboard already named %q (%s)", d.Name, existing.SourceFile)
	}

	if d.SourceFile == "" {
		d.SourceFile = filepath.Join(s.dir, slug(d.Name)+".yaml")
	}
	if err := writeDashboardFile(d); err != nil {
		return err
	}
	s.byName[d.Name] = d
	return nil
}

func writeDashboardFile(d *Dashboard) error {
	var doc yaml.Node
	if raw, err := os.ReadFile(d.SourceFile); err == nil {
		if uerr := yaml.Unmarshal(raw, &doc); uerr != nil {
			return fmt.Errorf("parse %s: %w", d.SourceFile, uerr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", d.SourceFile, err)
	}
	if len(doc.Content) == 0 {
		// Either a brand new file, or an existing one that was somehow
		// empty — either way, start from a fresh mapping to encode d
		// into rather than trying to patch nothing.
		var fresh yaml.Node
		if err := fresh.Encode(d); err != nil {
			return fmt.Errorf("encode %s: %w", d.SourceFile, err)
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&fresh}}
	} else if doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping at the top level", d.SourceFile)
	} else {
		setMappingKey(doc.Content[0], "name", d.Name)
		setMappingKey(doc.Content[0], "widgets", d.Widgets)
	}

	if err := os.MkdirAll(filepath.Dir(d.SourceFile), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(d.SourceFile), err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode %s: %w", d.SourceFile, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", d.SourceFile, err)
	}
	if err := os.WriteFile(d.SourceFile, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", d.SourceFile, err)
	}
	return nil
}

// setMappingKey replaces root's key's value with value (encoded fresh,
// so it always reflects the current Go value, never a stale Node),
// appending a new key at the end if root doesn't have it yet. Everything
// else in root — other keys, their comments — is untouched.
func setMappingKey(root *yaml.Node, key string, value any) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			var valueNode yaml.Node
			_ = valueNode.Encode(value)
			root.Content[i+1] = &valueNode
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	var valueNode yaml.Node
	_ = valueNode.Encode(value)
	root.Content = append(root.Content, keyNode, &valueNode)
}
