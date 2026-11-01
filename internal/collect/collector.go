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
// mutating the shared definition.
//
// Any collector error — timeout, non-zero exit, malformed output — is
// returned as an error with zero observations. It must never be read as a
// zero-valued measurement.
func Execute(ctx context.Context, runner Runner, serverName string, def *metric.Definition, timeout time.Duration) ([]Observation, error) {
	if def.Collector.Type != metric.CollectorShell {
		return nil, fmt.Errorf("metric %q: unsupported collector type %q", def.Name, def.Collector.Type)
	}

	out, err := runner.Run(ctx, def.Collector.Command, timeout)
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
