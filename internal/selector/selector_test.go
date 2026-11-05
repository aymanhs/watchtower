package selector

import "testing"

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"jumpbox",               // no '/'
		"/mem.free_pct",         // empty server
		"jumpbox/",              // empty metric path
		"jumpbox/mem",           // no '.'
		"jumpbox/a.b.c.d",       // too many segments
		"cluster:/mem.free_pct", // empty cluster pattern
		"jumpbox/mem.[",         // unterminated '[' glob class: invalid pattern
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q): expected error, got none", raw)
		}
	}
}

func TestMatchFixed(t *testing.T) {
	s := MustParse("jumpbox/mem.free_pct")
	if !s.Match(Candidate{Server: "jumpbox", Metric: "mem", Measurement: "free_pct"}) {
		t.Fatal("expected match")
	}
	if s.Match(Candidate{Server: "other", Metric: "mem", Measurement: "free_pct"}) {
		t.Fatal("expected no match: wrong server")
	}
	if s.Match(Candidate{Server: "jumpbox", Metric: "mem", Measurement: "used_pct"}) {
		t.Fatal("expected no match: wrong measurement")
	}
}

func TestMatchServerWildcard(t *testing.T) {
	s := MustParse("*/mem.free_pct")
	if !s.Match(Candidate{Server: "jumpbox", Metric: "mem", Measurement: "free_pct"}) {
		t.Fatal("expected match on jumpbox")
	}
	if !s.Match(Candidate{Server: "sbnats-nats1", Metric: "mem", Measurement: "free_pct"}) {
		t.Fatal("expected match on sbnats-nats1")
	}
	if s.Match(Candidate{Server: "jumpbox", Metric: "mem", Measurement: "used_pct"}) {
		t.Fatal("expected no match: wrong measurement")
	}
}

func TestMatchClusterPrefix(t *testing.T) {
	s := MustParse("cluster:connetic-nats/nats.connections")
	if !s.Match(Candidate{Server: "sbnats-nats1", Cluster: "connetic-nats", Metric: "nats", Measurement: "connections"}) {
		t.Fatal("expected match via cluster")
	}
	if s.Match(Candidate{Server: "sbnats-nats1", Cluster: "other-cluster", Metric: "nats", Measurement: "connections"}) {
		t.Fatal("expected no match: wrong cluster")
	}
	if s.Match(Candidate{Server: "sbnats-nats1", Cluster: "", Metric: "nats", Measurement: "connections"}) {
		t.Fatal("expected no match: server has no cluster at all")
	}
}

func TestMatchInstanceWildcard(t *testing.T) {
	s := MustParse("sbnats/servers.*.connections")
	if !s.Match(Candidate{Server: "sbnats", Metric: "servers", Instance: "nats1", Measurement: "connections"}) {
		t.Fatal("expected match on instance nats1")
	}
	if !s.Match(Candidate{Server: "sbnats", Metric: "servers", Instance: "nats2", Measurement: "connections"}) {
		t.Fatal("expected match on instance nats2")
	}
	// No instance at all on this candidate: a selector that explicitly
	// names an instance segment should not match an instance-less
	// measurement, even with a wildcard instance pattern.
	if s.Match(Candidate{Server: "sbnats", Metric: "servers", Instance: "", Measurement: "connections"}) {
		t.Fatal("expected no match: candidate has no instance")
	}
}

func TestMatchInstanceExact(t *testing.T) {
	s := MustParse("disk/disk.root.used_percent")
	if !s.Match(Candidate{Server: "disk", Metric: "disk", Instance: "root", Measurement: "used_percent"}) {
		t.Fatal("expected match on instance root")
	}
	if s.Match(Candidate{Server: "disk", Metric: "disk", Instance: "data", Measurement: "used_percent"}) {
		t.Fatal("expected no match: wrong instance")
	}
}

// A selector with no instance segment matches a measurement regardless
// of whether that measurement actually carries an instance — the
// instance-agnostic default (same spirit as metric.ResolveMeasurement
// itself not caring which volume "used_percent" came from unless asked).
func TestMatchNoInstanceSegmentIsInstanceAgnostic(t *testing.T) {
	s := MustParse("disk/disk.used_percent")
	if !s.Match(Candidate{Server: "disk", Metric: "disk", Instance: "root", Measurement: "used_percent"}) {
		t.Fatal("expected match even though candidate has an instance")
	}
	if !s.Match(Candidate{Server: "disk", Metric: "disk", Instance: "", Measurement: "used_percent"}) {
		t.Fatal("expected match with no instance at all")
	}
}

func TestMetricGlob(t *testing.T) {
	s := MustParse("jumpbox/*.free_pct")
	if !s.Match(Candidate{Server: "jumpbox", Metric: "mem", Measurement: "free_pct"}) {
		t.Fatal("expected match: metric wildcard")
	}
	if !s.Match(Candidate{Server: "jumpbox", Metric: "swap", Measurement: "free_pct"}) {
		t.Fatal("expected match: metric wildcard, different metric")
	}
}

func TestString(t *testing.T) {
	const raw = "jumpbox/mem.free_pct"
	if s := MustParse(raw); s.String() != raw {
		t.Fatalf("String() = %q, want %q", s.String(), raw)
	}
}
