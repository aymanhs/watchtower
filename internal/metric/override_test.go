package metric

import (
	"testing"
	"time"
)

func TestDefinition_Resolve(t *testing.T) {
	def := &Definition{
		Name:     "memory",
		Interval: Duration{10 * time.Second},
		Timeout:  Duration{5 * time.Second},
	}

	t.Run("no overrides keeps definition defaults", func(t *testing.T) {
		interval, timeout, cfg := def.Resolve()
		if interval != 10*time.Second || timeout != 5*time.Second {
			t.Fatalf("got interval=%v timeout=%v, want 10s/5s", interval, timeout)
		}
		if len(cfg) != 0 {
			t.Fatalf("got config %v, want empty", cfg)
		}
	})

	t.Run("later override wins over earlier", func(t *testing.T) {
		groupOv := Override{Interval: &Duration{15 * time.Second}}
		serverOv := Override{Interval: &Duration{20 * time.Second}}
		interval, timeout, _ := def.Resolve(groupOv, serverOv)
		if interval != 20*time.Second {
			t.Fatalf("interval = %v, want 20s (server override must win)", interval)
		}
		if timeout != 5*time.Second {
			t.Fatalf("timeout = %v, want unchanged 5s", timeout)
		}
	})

	t.Run("nil fields in a later override don't clobber an earlier one", func(t *testing.T) {
		groupOv := Override{Interval: &Duration{15 * time.Second}, Timeout: &Duration{3 * time.Second}}
		serverOv := Override{Interval: &Duration{20 * time.Second}} // Timeout left nil
		interval, timeout, _ := def.Resolve(groupOv, serverOv)
		if interval != 20*time.Second {
			t.Fatalf("interval = %v, want 20s", interval)
		}
		if timeout != 3*time.Second {
			t.Fatalf("timeout = %v, want 3s (group's value preserved since server didn't override it)", timeout)
		}
	})

	t.Run("config maps merge with later keys winning", func(t *testing.T) {
		groupOv := Override{Config: map[string]any{"endpoint": "group-default:8222", "extra": "kept"}}
		serverOv := Override{Config: map[string]any{"endpoint": "localhost:8222"}}
		_, _, cfg := def.Resolve(groupOv, serverOv)
		if cfg["endpoint"] != "localhost:8222" {
			t.Errorf("config[endpoint] = %v, want localhost:8222", cfg["endpoint"])
		}
		if cfg["extra"] != "kept" {
			t.Errorf("config[extra] = %v, want kept", cfg["extra"])
		}
	})
}
