package collect

import (
	"context"
	"strings"
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

type objRunner string

func (f objRunner) Run(context.Context, string, time.Duration) (string, error) { return string(f), nil }

func TestExecuteObject(t *testing.T) {
	def := &metric.Definition{Name: "o", Kind: metric.KindObject, MaxBytes: 16,
		Collector: metric.Collector{Type: metric.CollectorShell, Command: "x"}}
	got, err := ExecuteObject(context.Background(), objRunner("[ {\"a\": 1} ]\n"), def, time.Second, nil)
	if err != nil || string(got) != `[{"a":1}]` {
		t.Fatalf("got %s, %v", got, err)
	}
	if _, err := ExecuteObject(context.Background(), objRunner("not json"), def, time.Second, nil); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	big := objRunner(`{"k":"` + strings.Repeat("x", 40) + `"}`)
	if _, err := ExecuteObject(context.Background(), big, def, time.Second, nil); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversize: %v", err)
	}
}
