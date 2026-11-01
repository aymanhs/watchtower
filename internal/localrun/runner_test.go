package localrun

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunner_Success(t *testing.T) {
	r := New()
	out, err := r.Run(context.Background(), "echo value=42", time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "value=42\n" {
		t.Fatalf("got %q, want %q", out, "value=42\n")
	}
}

func TestRunner_NonZeroExitIsError(t *testing.T) {
	r := New()
	out, err := r.Run(context.Background(), "echo oops >&2; exit 3", time.Second)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if out != "" {
		t.Fatalf("expected empty output on error, got %q", out)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("error = %v, want it to include captured stderr (\"oops\")", err)
	}
}

func TestRunner_TimeoutIsError(t *testing.T) {
	r := New()
	_, err := r.Run(context.Background(), "sleep 2", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want it to mention the timeout", err)
	}
}

func TestRunner_CommandNotFoundIsError(t *testing.T) {
	r := New()
	_, err := r.Run(context.Background(), "this-command-does-not-exist-anywhere", time.Second)
	if err == nil {
		t.Fatal("expected error for a nonexistent command, got nil")
	}
}
