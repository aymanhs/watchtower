// Package collect executes one metric definition's collector and turns its
// output into generic Observations. It knows nothing about SSH, storage,
// events or the GUI — only how to run a command or fetch a URL, and how
// to parse what comes back.
package collect

import (
	"context"
	"fmt"
	"net"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// Runner executes a shell command with a timeout and returns its stdout.
// *sshmgr.Client and *localrun.Runner both satisfy this; the interface
// keeps collect decoupled from, and testable without, either.
type Runner interface {
	Run(ctx context.Context, command string, timeout time.Duration) (string, error)
}

// Dialer opens a TCP connection as a CollectorHTTP fetch should see the
// network — tunneled through an SSH session for an ssh-type server (so a
// loopback-bound endpoint is reachable exactly as if running on that
// host), or dialed directly for a "local" one. *sshmgr.Client and
// *localrun.Runner both satisfy this too.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Backend is everything a server needs to provide to run either kind of
// collector. One concrete type (sshmgr.Client, localrun.Runner) commonly
// satisfies both halves, but Execute only ever needs whichever half the
// definition's Collector.Type actually calls for.
type Backend interface {
	Runner
	Dialer
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

// Execute runs def's collector once against serverName via backend,
// parses its output and returns one Observation per measurement the
// definition declares that was actually present in the result. timeout
// overrides def.Timeout so callers can apply server-specific overrides
// without mutating the shared definition. config is this server's
// resolved collector parameters (metric default -> group -> server
// override, already merged by metric.Definition.Resolve), substituted
// into the collector's command/url template — e.g. a NATS server's
// "endpoint" — empty for the common case of a definition with no
// parameters at all.
//
// Any collector error — timeout, non-zero exit, a non-200 response,
// malformed output, a template referencing a config key this server
// didn't supply — is returned as an error with zero observations. It
// must never be read as a zero-valued measurement.
func Execute(ctx context.Context, backend Backend, serverName string, def *metric.Definition, timeout time.Duration, config map[string]any) ([]Observation, error) {
	switch def.Collector.Type {
	case metric.CollectorShell:
		return executeShell(ctx, backend, serverName, def, timeout, config)
	case metric.CollectorHTTP:
		return executeHTTP(ctx, backend, serverName, def, timeout, config)
	default:
		return nil, fmt.Errorf("metric %q: unsupported collector type %q", def.Name, def.Collector.Type)
	}
}

func executeShell(ctx context.Context, runner Runner, serverName string, def *metric.Definition, timeout time.Duration, config map[string]any) ([]Observation, error) {
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

	return observationsFrom(serverName, def, values), nil
}

// observationsFrom emits one Observation per key in values that resolves
// against def.Metrics — either a direct match (the common case) or, for a
// collector that discovers its own instances at runtime (disk.yaml's
// per-volume stats: one declared "used_percent", a real "root_used_percent"
// / "data_used_percent" per volume actually found this cycle),
// Definition.ResolveMeasurement's instance-prefixed match. Anything in
// values that resolves to neither — an unexpected line from the
// collector's own output, a typo — is silently skipped, not an error:
// matches the collector contract both executeShell and executeHTTP share,
// a missing/unrecognized key just means no Observation for it this cycle.
func observationsFrom(serverName string, def *metric.Definition, values map[string]float64) []Observation {
	now := time.Now()
	observations := make([]Observation, 0, len(values))
	for key, val := range values {
		if _, _, _, ok := def.ResolveMeasurement(key); !ok {
			continue
		}
		observations = append(observations, Observation{
			Server:    serverName,
			Metric:    def.Name + "." + key,
			Value:     val,
			Timestamp: now,
		})
	}
	return observations
}
