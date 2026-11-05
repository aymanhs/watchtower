// Package selector parses and matches the pattern string a dashboard
// widget uses to name which metrics it wants, now that a dashboard is no
// longer scoped to one server (see internal/dashboard). A selector is a
// plain string of the shape:
//
//	<server>/<metric>.<measurement>
//	<server>/<metric>.<instance>.<measurement>
//
// where <server> is a server name, "*", a glob (shell-style, via
// path.Match: "*", "?", "[...]"), or "cluster:<pattern>" to match against
// a server's Cluster label instead of its name; <metric> is a metric
// definition name (also glob-matchable); and the measurement half is
// either just the measurement key, or an instance segment then the
// measurement key for a per-instance measurement (metric.ResolveMeasurement's
// "root_used_percent" style) — e.g. "disk/disk.*.used_percent" matches
// every volume's used_percent on server "disk". A selector with no
// instance segment ("jumpbox/mem.free_pct") matches that measurement
// regardless of whether the real data happens to carry an instance or
// not — the common, instance-agnostic case.
//
// This package is purely textual: it knows nothing about servers, state
// snapshots or storage. Match is called once per candidate tuple by
// whatever layer has those tuples (internal/dashboard's resolver).
package selector

import (
	"fmt"
	"path"
	"strings"
)

// Selector is a parsed selector pattern, ready to Match against
// candidate (server, metric, instance, measurement) tuples.
type Selector struct {
	raw string

	clusterMode   bool   // server pattern matches Cluster, not Name
	serverPattern string // "*", exact, or glob

	metricPattern string // "*", exact, or glob

	hasInstance     bool   // selector named an instance segment at all
	instancePattern string // only meaningful if hasInstance
	measurePattern  string
}

// String returns the original selector text, unchanged.
func (s Selector) String() string { return s.raw }

// Parse parses raw into a Selector, or returns an error describing what's
// wrong with it. Parsing never touches live data — a selector can be
// parsed and saved even if nothing currently matches it (same spirit as
// dashboard.Widget.Validate not resolving against a snapshot).
func Parse(raw string) (Selector, error) {
	server, metricPath, ok := strings.Cut(raw, "/")
	if !ok {
		return Selector{}, fmt.Errorf("selector %q: missing '/' between server and metric", raw)
	}
	if server == "" {
		return Selector{}, fmt.Errorf("selector %q: empty server pattern", raw)
	}
	if metricPath == "" {
		return Selector{}, fmt.Errorf("selector %q: empty metric path", raw)
	}

	s := Selector{raw: raw}
	s.serverPattern, s.clusterMode = strings.CutPrefix(server, "cluster:")
	if s.clusterMode && s.serverPattern == "" {
		return Selector{}, fmt.Errorf("selector %q: empty cluster pattern", raw)
	}

	segs := strings.Split(metricPath, ".")
	switch len(segs) {
	case 1:
		return Selector{}, fmt.Errorf("selector %q: metric path needs at least <metric>.<measurement>", raw)
	case 2:
		s.metricPattern, s.measurePattern = segs[0], segs[1]
	case 3:
		s.metricPattern, s.instancePattern, s.measurePattern = segs[0], segs[1], segs[2]
		s.hasInstance = true
	default:
		return Selector{}, fmt.Errorf("selector %q: metric path has too many '.' segments (want <metric>.<measurement> or <metric>.<instance>.<measurement>)", raw)
	}
	for _, pat := range []string{s.serverPattern, s.metricPattern, s.instancePattern, s.measurePattern} {
		if pat == "" {
			continue
		}
		if _, err := path.Match(pat, ""); err != nil {
			return Selector{}, fmt.Errorf("selector %q: invalid pattern %q: %w", raw, pat, err)
		}
	}
	return s, nil
}

// MustParse is Parse, panicking on error — for literal selectors built in
// code (tests, migration) where a parse failure is a programmer error.
func MustParse(raw string) Selector {
	s, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return s
}

// Candidate is one concrete (server, metric, measurement) tuple a
// selector is matched against — exactly what a resolver builds from a
// state.ServerView, one per MeasurementView, with Instance set only when
// metric.ResolveMeasurement decomposed the key (baseKey, instance).
type Candidate struct {
	Server      string
	Cluster     string // "" if the server isn't in a cluster
	Metric      string
	Instance    string // "" if this measurement isn't per-instance
	Measurement string // the base measurement key (ResolveMeasurement's baseKey)
}

// Match reports whether c satisfies s.
func (s Selector) Match(c Candidate) bool {
	serverVal := c.Server
	if s.clusterMode {
		serverVal = c.Cluster
	}
	if !glob(s.serverPattern, serverVal) {
		return false
	}
	if !glob(s.metricPattern, c.Metric) {
		return false
	}
	if s.hasInstance {
		if c.Instance == "" || !glob(s.instancePattern, c.Instance) {
			return false
		}
	}
	return glob(s.measurePattern, c.Measurement)
}

// glob matches pattern against name with path.Match, treating a parse
// error (already rejected at Parse time, so only reachable if someone
// hand-builds a Selector) as no match rather than panicking.
func glob(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
