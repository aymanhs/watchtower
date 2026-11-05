package dashboard

import (
	"sort"

	"aymanhs/watchtower/internal/selector"
	"aymanhs/watchtower/internal/state"
)

// Match is one concrete (server, metric, measurement) a SeriesRef's
// selector resolved to against a live state snapshot — a selector
// scoped to one fixed server and measurement resolves to at most one of
// these; a wildcard or cluster selector, to as many as currently match.
type Match struct {
	Server       string
	Cluster      string
	Metric       string
	MetricStatus state.Status
	Measurement  state.MeasurementView // Key is the raw measurement key, e.g. "root_used_percent"

	// HistoryKey is this match's internal/storage history key —
	// Server+"/"+Metric+"."+Measurement.Key, exactly storage's own
	// Observation.FullName() shape, since that's the raw key history was
	// actually appended under (never the decomposed baseKey/instance).
	HistoryKey string
}

// Resolve matches ref's selector against every server in servers,
// returning every (server, metric, measurement) tuple it currently
// matches, sorted by server then metric then measurement key for a
// stable render order (a widget's layout shouldn't reshuffle from one
// frame to the next just because map iteration order did). An
// unparseable selector (shouldn't happen — Widget.Validate already
// rejects one at save time) resolves to no matches, rather than
// panicking or erroring — same "degrade visibly to nothing found" rule
// as a selector that parses fine but simply isn't reporting yet.
//
// A measurement whose raw key follows metric.Definition.ResolveMeasurement's
// per-instance suffix convention ("root_used_percent" decomposing to
// baseKey "used_percent", instance "root") is tried against every
// underscore-split position as a candidate (instance, baseKey) pair, in
// addition to the plain no-instance candidate — this package has no
// metric.Registry to ask which suffix a definition actually declared, so
// "did any plausible split satisfy the selector" stands in for it. That
// keeps a selector's instance segment (e.g. "disk/disk.*.used_percent")
// working against real per-instance measurements without this
// presentation-level package needing to know anything about collectors
// or metric definitions.
func (ref SeriesRef) Resolve(servers []state.ServerView) []Match {
	sel, err := ref.Parsed()
	if err != nil {
		return nil
	}
	var out []Match
	for _, sv := range servers {
		for _, m := range sv.Metrics {
			for _, meas := range m.Measurements {
				if !matchesAnySplit(sel, sv, m, meas) {
					continue
				}
				out = append(out, Match{
					Server:       sv.Name,
					Cluster:      sv.Cluster,
					Metric:       m.Name,
					MetricStatus: m.Status,
					Measurement:  meas,
					HistoryKey:   sv.Name + "/" + m.Name + "." + meas.Key,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return out[i].Measurement.Key < out[j].Measurement.Key
	})
	return out
}

// ResolveWidget resolves every Series in w against servers, in order,
// concatenating all their Matches — a chart or pie widget naturally
// wants every series' matches flattened into one set of lines/slices;
// number/gauge render each Match as its own sub-tile, same flattened
// list either way.
func ResolveWidget(w Widget, servers []state.ServerView) []Match {
	var out []Match
	for _, ref := range w.Series {
		out = append(out, ref.Resolve(servers)...)
	}
	return out
}

// matchesAnySplit reports whether (sv, m, meas) satisfies sel, trying
// the plain no-instance candidate first, then every underscore-split
// position of meas.Key as an (instance, baseKey) candidate.
func matchesAnySplit(sel selector.Selector, sv state.ServerView, m state.MetricView, meas state.MeasurementView) bool {
	base := selector.Candidate{Server: sv.Name, Cluster: sv.Cluster, Metric: m.Name, Measurement: meas.Key}
	if sel.Match(base) {
		return true
	}
	key := meas.Key
	for i := 0; i < len(key); i++ {
		if key[i] != '_' {
			continue
		}
		instance, baseKey := key[:i], key[i+1:]
		if instance == "" || baseKey == "" {
			continue
		}
		cand := selector.Candidate{Server: sv.Name, Cluster: sv.Cluster, Metric: m.Name, Instance: instance, Measurement: baseKey}
		if sel.Match(cand) {
			return true
		}
	}
	return false
}
