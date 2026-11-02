package server

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"aymanhs/watchtower/internal/dashboard"
)

// SaveDashboard rewrites cfg's dashboard widgets into its SourceFile and
// updates cfg.Dashboard in memory on success — the GUI's add/remove-widget
// controls call this so "the dashboard is config-as-code, in the server's
// own YAML" (the explicit brief here) stays true even when a person never
// hand-edits the file themselves.
//
// It edits the existing document's yaml.Node tree rather than
// yaml.Marshal(cfg) wholesale: a server YAML file is hand-written and
// sometimes hand-commented (see data/servers/sbnats-nats*.yaml's notes on
// placeholder monitoring ports) — a full struct remarshal would silently
// discard every comment in the file. Only the top-level "dashboard" key
// is touched; everything else keeps its original text, comments included.
func SaveDashboard(cfg *Config, widgets []dashboard.Widget) error {
	if cfg.SourceFile == "" {
		return fmt.Errorf("server %q: no source file recorded, can't save its dashboard", cfg.Name)
	}
	for i, w := range widgets {
		if err := w.Validate(); err != nil {
			return fmt.Errorf("server %q: refusing to save an invalid widget %d: %w", cfg.Name, i, err)
		}
	}

	raw, err := os.ReadFile(cfg.SourceFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.SourceFile, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", cfg.SourceFile, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping at the top level", cfg.SourceFile)
	}
	root := doc.Content[0]

	setDashboardKey(root, widgets)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) // matches this project's own server YAML convention
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode %s: %w", cfg.SourceFile, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", cfg.SourceFile, err)
	}
	if err := os.WriteFile(cfg.SourceFile, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", cfg.SourceFile, err)
	}
	cfg.Dashboard = widgets
	return nil
}

// setDashboardKey replaces root's "dashboard" mapping entry with widgets
// (encoded fresh, so it always reflects the current Go value, never a
// stale Node), removing the key entirely when widgets is empty — an
// empty "dashboard: []" sitting in every server file forever would be
// noise for the common case of a server with no dashboard at all.
func setDashboardKey(root *yaml.Node, widgets []dashboard.Widget) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "dashboard" {
			if len(widgets) == 0 {
				root.Content = append(root.Content[:i:i], root.Content[i+2:]...)
				return
			}
			var valueNode yaml.Node
			_ = valueNode.Encode(widgets)
			root.Content[i+1] = &valueNode
			return
		}
	}
	if len(widgets) == 0 {
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "dashboard"}
	var valueNode yaml.Node
	_ = valueNode.Encode(widgets)
	root.Content = append(root.Content, keyNode, &valueNode)
}
