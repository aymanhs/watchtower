// Package app wires the independent engine components (metric/group/server
// config, SSH, scheduler, event engine, state store) into the set of
// scheduler.Tasks a presentation layer runs. Both cmd/watchtower (console)
// and cmd/watchtower-gui (MyGo GUI) call Bootstrap and get back the same
// engine; only how they present the result differs.
package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// MetricTask adapts one (server, metric definition) pair to scheduler.Task.
// It is the single place that bridges SSH/collect to the event engine and,
// optionally, to a state.Store and/or console output — a presentation
// layer needs nothing more than a *state.Store to render from.
type MetricTask struct {
	ServerName                string
	Def                       *metric.Definition
	Client                    collect.Backend // typically *sshmgr.Client or *localrun.Runner; an interface so this is testable without a real SSH connection
	CollectInterval, Deadline time.Duration   // Deadline: per-execution timeout (named to avoid colliding with the Timeout() method below)
	// Config is this (server, metric) pair's fully-resolved collector
	// parameters (see internal/app.effective), substituted into the
	// collector's command template. Empty for the common case of a
	// definition with no parameters at all.
	Config map[string]any
	Events *event.Engine

	// Store, if non-nil, receives this task's results for a GUI (or any
	// other presentation) to read via Store.Snapshot.
	Store *state.Store
	// History, if non-nil, persists every successful observation for
	// later range queries (charts) — independent of Store, which only
	// ever holds the latest value. A storage error here is logged, not
	// treated as a collection failure: a metric that collected fine but
	// failed to persist its history is not the same thing as a metric
	// that failed to collect at all.
	History *storage.Store
	// Print, if true, writes observations and event transitions to stdout
	// — the console presentation's entire rendering.
	Print bool
}

func (t *MetricTask) Name() string            { return t.ServerName + "/" + t.Def.Name }
func (t *MetricTask) Interval() time.Duration { return t.CollectInterval }
func (t *MetricTask) Timeout() time.Duration  { return t.Deadline }

func (t *MetricTask) Run(ctx context.Context) error {
	obs, err := collect.Execute(ctx, t.Client, t.ServerName, t.Def, t.Deadline, t.Config)
	now := time.Now()

	if opEv := t.Events.EvaluateCollectorResult(t.ServerName, t.Def.Name, err, now); opEv != nil && t.Print {
		printEvent(*opEv)
	}

	if err != nil {
		if t.Store != nil {
			t.Store.MarkStale(t.ServerName, t.Def.Name, err.Error(), now)
		}
		return err
	}

	// Raw values of every measurement this cycle, so a display.scale's
	// max_ref can look up a sibling (e.g. "used" scaled against "total")
	// regardless of which order observations happen to come in.
	rawValues := make(map[string]float64, len(obs))
	for _, o := range obs {
		rawValues[strings.TrimPrefix(o.Metric, t.Def.Name+".")] = o.Value
	}

	measurementSeverity := make(map[string]metric.Severity, len(obs))
	measurements := make([]state.MeasurementView, 0, len(obs))
	for _, o := range obs {
		if t.Print {
			fmt.Printf("%-32s = %-16v %s\n", o.FullName(), o.Value, o.Timestamp.Format(time.RFC3339))
		}
		measurement := strings.TrimPrefix(o.Metric, t.Def.Name+".")
		def, baseKey, instance, ok := t.Def.ResolveMeasurement(measurement)
		// ruleKey is what event rules and zone derivation are actually
		// declared against — baseKey for a resolved measurement (the
		// static case: baseKey == measurement; the per-instance case:
		// baseKey is the declared key after the last ".", e.g.
		// "used_percent" for "root.used_percent"). measurement itself
		// stays the per-alarm identity either way, so "disk.root" and
		// "disk.data" alarm independently instead of sharing one
		// ambiguous state.
		ruleKey := measurement
		if ok {
			ruleKey = baseKey
		}
		if ev := t.Events.EvaluateThreshold(t.ServerName, t.Def, measurement, ruleKey, o.Value, o.Timestamp); ev != nil && t.Print {
			printEvent(*ev)
		}
		if t.History != nil {
			if err := t.History.Append(o.FullName(), o.Timestamp, o.Value); err != nil {
				log.Printf("[%s] history: %v", t.Name(), err)
			}
		}
		measurementSeverity[measurement] = t.Events.CurrentSeverity(t.ServerName, t.Def.Name+"."+measurement)

		mv := state.MeasurementView{Key: measurement, Value: o.Value}
		if !ok {
			measurements = append(measurements, mv) // shouldn't happen; collect.Execute already filters to known keys
			continue
		}
		mv.Unit = def.Unit
		mv.Type = def.Type
		if disp := def.Display; disp != nil {
			mv.Widget = disp.Widget
			mv.Primary = disp.Primary
			mv.Label = disp.Label
			mv.Format = disp.Format
			if instance != "" && disp.Label != "" {
				// "Used" -> "Used (root)" — the dashboard-free, built-in
				// metric line needs some way to tell volumes apart too,
				// not just the dashboard's own per-series labels.
				mv.Label = disp.Label + " (" + instance + ")"
			}
			scaleCfg := disp.Scale
			if scaleCfg != nil && instance != "" && scaleCfg.MaxRef != "" {
				// max_ref: total means "this instance's own total", not
				// a single global one shared across every volume —
				// qualify it the same way the collector itself named
				// this instance's sibling measurements: instance+"."+key,
				// matching rawValues' own keys (built straight from
				// o.Metric, so already in whatever dotted shape the
				// collector actually emitted).
				scoped := *scaleCfg
				scoped.MaxRef = instance + "." + scoped.MaxRef
				scaleCfg = &scoped
			}
			scale, err := state.ResolveScale(scaleCfg, rawValues)
			if err != nil {
				// A bad max_ref this cycle (e.g. the sibling momentarily
				// missing) degrades to plain text rather than failing the
				// whole collection — one measurement's display problem
				// must not look like a collector failure.
				if t.Print {
					fmt.Printf("[%s/%s] display: %v\n", t.ServerName, t.Def.Name, err)
				}
			} else {
				mv.Scale = scale
				// Both gauge and bar can show zone bands when the
				// measurement has event rules to derive them from; a
				// measurement with no rules just gets no Zones either way
				// (ResolveZones returns nil), which both widgets already
				// render as their plain unzoned fallback.
				if scale != nil && (disp.Widget == metric.WidgetGauge || disp.Widget == metric.WidgetBar) {
					mv.Zones = state.ResolveZones(rulesFor(t.Def, ruleKey), *scale)
				}
			}
		}
		measurements = append(measurements, mv)
	}

	if t.Store != nil {
		t.Store.UpdateSuccess(t.ServerName, t.Def.Name, measurements, measurementSeverity, now)
	}
	return nil
}

// rulesFor returns def's event rules that target one measurement.
func rulesFor(def *metric.Definition, measurement string) []metric.EventRule {
	var out []metric.EventRule
	for _, r := range def.Events {
		if r.Measurement == measurement {
			out = append(out, r)
		}
	}
	return out
}

func printEvent(ev event.Event) {
	switch ev.Kind {
	case event.KindRaised:
		fmt.Printf("!!! EVENT  %-8s %-10s %s/%-24s value=%v threshold=%v %q\n",
			ev.Severity, ev.Kind, ev.Server, ev.Metric, ev.Value, ev.Threshold, ev.Message)
	case event.KindRecovered:
		sev := ev.Severity
		if sev == "" {
			sev = "normal"
		}
		fmt.Printf("!!! EVENT  %-8s %-10s %s/%-24s value=%v %q\n",
			sev, ev.Kind, ev.Server, ev.Metric, ev.Value, ev.Message)
	}
}
