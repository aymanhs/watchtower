package metric

import "testing"

func TestCollector_Render_NoTemplateIsNoOp(t *testing.T) {
	c := Collector{Command: "free -b | awk '{print $1}'"}
	out, err := c.Render(nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != c.Command {
		t.Fatalf("got %q, want unchanged %q", out, c.Command)
	}
}

func TestCollector_Render_SubstitutesConfig(t *testing.T) {
	c := Collector{Command: "curl -s http://{{.endpoint}}/varz"}
	out, err := c.Render(map[string]any{"endpoint": "localhost:8222"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "curl -s http://localhost:8222/varz"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCollector_Render_MissingConfigKeyErrors(t *testing.T) {
	c := Collector{Command: "curl -s http://{{.endpoint}}/varz"}
	if _, err := c.Render(map[string]any{}); err == nil {
		t.Fatal("expected error when config doesn't supply a referenced key, got nil")
	}
}

func TestDefinition_Validate_RejectsMalformedTemplate(t *testing.T) {
	d := baseDefForDisplay()
	d.Collector.Command = "curl {{ .endpoint " // unclosed action
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for malformed command template, got nil")
	}
}

func TestDefinition_Validate_AcceptsTemplateWithNoConfigYet(t *testing.T) {
	// A template referencing {{.endpoint}} is syntactically valid even
	// though no config is supplied until a server resolves one — that's
	// a runtime concern (Render), not a load-time one.
	d := baseDefForDisplay()
	d.Collector.Command = "curl -s http://{{.endpoint}}/varz"
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate should accept a template with unresolved config refs: %v", err)
	}
}
