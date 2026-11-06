package collect

import (
	"strings"
	"testing"
)

func TestParseLineOutput_Valid(t *testing.T) {
	got, err := ParseLineOutput("total=100\nused=40\nfree=60\n\n")
	if err != nil {
		t.Fatalf("ParseLineOutput: %v", err)
	}
	want := map[string]float64{"total": 100, "used": 40, "free": 60}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestParseLineOutput_RejectsMissingEquals(t *testing.T) {
	if _, err := ParseLineOutput("total 100\n"); err == nil {
		t.Fatal("expected error for line without '=', got nil")
	}
}

func TestParseLineOutput_RejectsNonNumericValue(t *testing.T) {
	if _, err := ParseLineOutput("total=abc\n"); err == nil {
		t.Fatal("expected error for non-numeric value, got nil")
	}
}

func TestParseLineOutput_RejectsDuplicateKey(t *testing.T) {
	if _, err := ParseLineOutput("total=1\ntotal=2\n"); err == nil {
		t.Fatal("expected error for duplicate key, got nil")
	}
}

func TestParseLineOutput_RejectsEmptyKey(t *testing.T) {
	if _, err := ParseLineOutput("=5\n"); err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt(" a=1\nb  oops\n"); got != "a=1 | b oops" {
		t.Errorf("Excerpt = %q", got)
	}
	if got := Excerpt(strings.Repeat("x", 1000)); len(got) > maxExcerpt+len("…") {
		t.Errorf("not capped: %d", len(got))
	}
}
