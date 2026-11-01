// Package collect executes one metric definition's collector and turns its
// output into generic Observations. It knows nothing about SSH, storage,
// events or the GUI — only how to run a command and parse its output.
package collect

import (
	"context"
	"fmt"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// Runner executes a command with a timeout and returns its stdout.
// *sshmgr.Client satisfies this; the interface keeps collect decoupled
// from and testable without a real SSH connection.
type Runner interface {
	Run(ctx context.Context, command string, timeout time.Duration) (string, error)
}

// Observation is one generic, named, timestamped numeric measurement — the
// only thing the collection engine deals in. The engine never interprets
// Metric's meaning.
type Observation struct {
	Server    string
	Metric    string // e.g. "memory.free"
	Value     float64
	Timestamp time.Time
}

// FullName returns the hierarchical identifier: server/metric.key.
func (o Observation) FullName() string {
	return o.Server + "/" + o.Metric
}

// Execute runs def's collector once against serverName via runner, parses
// its output and returns one Observation per measurement the definition
// declares that was actually present in the output. timeout overrides
// def.Timeout so callers can apply server-specific overrides without
// mutating the shared definition. config is this server's resolved
// collector parameters (metric default -> group -> server override,
// already merged by metric.Definition.Resolve) substituted into the
// collector's command template — e.g. a NATS server's "endpoint" — empty
// for the common case of a definition with no parameters at all.
//
// Any collector error — timeout, non-zero exit, malformed output, a
// command template referencing a config key this server didn't supply —
// is returned as an error with zero observations. It must never be read
// as a zero-valued measurement.
func Execute(ctx context.Context, runner Runner, serverName string, def *metric.Definition, timeout time.Duration, config map[string]any) ([]Observation, error) {
	if def.Collector.Type != metric.CollectorShell {
		return nil, fmt.Errorf("metric %q: unsupported collector type %q", def.Name, def.Collector.Type)
	}

	command, err := def.Collector.Render(config)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: %w", def.Name, serverName, err)
	}

	out, err := runner.Run(ctx, command, timeout)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: %w", def.Name, serverName, err)
	}

	values, err := ParseLineOutput(out)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: %w", def.Name, serverName, err)
	}

	now := time.Now()
	observations := make([]Observation, 0, len(def.Metrics))
	for key := range def.Metrics {
		val, ok := values[key]
		if !ok {
			continue // not present this cycle; not an error (e.g. optional fields)
		}
		observations = append(observations, Observation{
			Server:    serverName,
			Metric:    def.Name + "." + key,
			Value:     val,
			Timestamp: now,
		})
	}
	return observations, nil
}
