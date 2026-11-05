package dashboard

import (
	"sort"
	"strings"

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
	Measurement  state.MeasurementView // Key is the raw measurement key, e.g. "root.used_percent"

	// Instance is the per-instance qualifier this match's measurement
	// decomposes to — e.g. "root" for disk's "root.used_percent" — "" if
	// the measurement isn't per-instance at all, or matched without
	// needing to decompose one (a selector with no instance segment
	// matches instance-agnostically; see SeriesRef.Resolve). A
	// presentation wanting to tell several matches apart by something
	// more concrete than the measurement's own label (a dashboard bars
	// widget labeling each bar by mount point, say) reads this.
	Instance string

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
// per-instance convention ("root.used_percent" decomposing to baseKey
// "used_percent", instance "root", split at the last ".") is tried as
// that (instance, baseKey) candidate, in addition to the plain
// no-instance candidate (the raw key matched as-is) — this package has
// no metric.Registry to confirm "used_percent" is actually a declared
// key, so it doesn't need to: the selector itself only cares whether its
// own measurement pattern matches whatever baseKey this produces. That
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
				instance, ok := matchesAnySplit(sel, sv, m, meas)
				if !ok {
					continue
				}
				out = append(out, Match{
					Server:       sv.Name,
					Cluster:      sv.Cluster,
					Metric:       m.Name,
					MetricStatus: m.Status,
					Measurement:  meas,
					Instance:     instance,
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

// matchesAnySplit reports whether (sv, m, meas) satisfies sel, trying the
// plain no-instance candidate first (meas.Key matched as-is), then —
// mirroring metric.Definition.ResolveMeasurement — the split at meas.Key's
// last "." as an (instance, baseKey) candidate, if it has one. Returns
// whichever instance (if any) made it match, "" for the no-instance case.
func matchesAnySplit(sel selector.Selector, sv state.ServerView, m state.MetricView, meas state.MeasurementView) (instance string, ok bool) {
	base := selector.Candidate{Server: sv.Name, Cluster: sv.Cluster, Metric: m.Name, Measurement: meas.Key}
	if sel.Match(base) {
		return "", true
	}
	if i := strings.LastIndexByte(meas.Key, '.'); i >= 0 {
		inst, baseKey := meas.Key[:i], meas.Key[i+1:]
		if inst != "" && baseKey != "" {
			cand := selector.Candidate{Server: sv.Name, Cluster: sv.Cluster, Metric: m.Name, Instance: inst, Measurement: baseKey}
			if sel.Match(cand) {
				return inst, true
			}
		}
	}
	return "", false
}
