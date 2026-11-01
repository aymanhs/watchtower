// Package app wires the independent engine components (metric/group/server
// config, SSH, scheduler, event engine, state store) into the set of
// scheduler.Tasks a presentation layer runs. Both cmd/watchtower (console)
// and cmd/watchtower-gui (MyGo GUI) call Bootstrap and get back the same
// engine; only how they present the result differs.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/sshmgr"
	"aymanhs/watchtower/internal/state"
)

// MetricTask adapts one (server, metric definition) pair to scheduler.Task.
// It is the single place that bridges SSH/collect to the event engine and,
// optionally, to a state.Store and/or console output — a presentation
// layer needs nothing more than a *state.Store to render from.
type MetricTask struct {
	ServerName                string
	Def                       *metric.Definition
	Client                    *sshmgr.Client
	CollectInterval, Deadline time.Duration // Deadline: per-execution timeout (named to avoid colliding with the Timeout() method below)
	Events                    *event.Engine

	// Store, if non-nil, receives this task's results for a GUI (or any
	// other presentation) to read via Store.Snapshot.
	Store *state.Store
	// Print, if true, writes observations and event transitions to stdout
	// — the console presentation's entire rendering.
	Print bool
}

func (t *MetricTask) Name() string            { return t.ServerName + "/" + t.Def.Name }
func (t *MetricTask) Interval() time.Duration { return t.CollectInterval }
func (t *MetricTask) Timeout() time.Duration  { return t.Deadline }

func (t *MetricTask) Run(ctx context.Context) error {
	obs, err := collect.Execute(ctx, t.Client, t.ServerName, t.Def, t.Deadline)
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

	measurementSeverity := make(map[string]metric.Severity, len(obs))
	measurements := make([]state.MeasurementView, 0, len(obs))
	for _, o := range obs {
		if t.Print {
			fmt.Printf("%-32s = %-16v %s\n", o.FullName(), o.Value, o.Timestamp.Format(time.RFC3339))
		}
		measurement := strings.TrimPrefix(o.Metric, t.Def.Name+".")
		if ev := t.Events.EvaluateThreshold(t.ServerName, t.Def, measurement, o.Value, o.Timestamp); ev != nil && t.Print {
			printEvent(*ev)
		}
		measurementSeverity[measurement] = t.Events.CurrentSeverity(t.ServerName, t.Def.Name+"."+measurement)
		unit := ""
		if m, ok := t.Def.Metrics[measurement]; ok {
			unit = m.Unit
		}
		measurements = append(measurements, state.MeasurementView{Key: measurement, Value: o.Value, Unit: unit})
	}

	if t.Store != nil {
		t.Store.UpdateSuccess(t.ServerName, t.Def.Name, measurements, measurementSeverity, now)
	}
	return nil
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
