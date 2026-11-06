package metric

import (
	"testing"
	"time"
)

func httpDefFor(t *testing.T) *Definition {
	t.Helper()
	return &Definition{
		Name:     "nats",
		Groups:   []string{"messaging"},
		Interval: Duration{10 * time.Second},
		Timeout:  Duration{5 * time.Second},
		Collector: Collector{
			Type: CollectorHTTP,
			URL:  "http://{{.endpoint}}/varz",
		},
	}
}

func TestHTTPCollector_ValidURLAccepted(t *testing.T) {
	d := httpDefFor(t)
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestHTTPCollector_RequiresURL(t *testing.T) {
	d := httpDefFor(t)
	d.Collector.URL = ""
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for missing url, got nil")
	}
}

func TestHTTPCollector_RejectsMalformedURLTemplate(t *testing.T) {
	d := httpDefFor(t)
	d.Collector.URL = "http://{{ .endpoint /varz"
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for malformed url template, got nil")
	}
}

func TestHTTPCollector_RejectsEmptyFieldMapping(t *testing.T) {
	d := httpDefFor(t)
	d.Collector.Fields = map[string]string{"connections": ""}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for empty fields[key], got nil")
	}
}

func TestCollector_RenderURL(t *testing.T) {
	c := Collector{Type: CollectorHTTP, URL: "http://{{.endpoint}}/varz"}
	out, err := c.RenderURL(map[string]any{"endpoint": "localhost:8222"})
	if err != nil {
		t.Fatalf("RenderURL: %v", err)
	}
	if out != "http://localhost:8222/varz" {
		t.Fatalf("got %q", out)
	}
}
