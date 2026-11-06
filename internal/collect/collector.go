// Package collect executes one metric definition's collector and turns its
// output into generic Observations. It knows nothing about SSH, storage,
// events or the GUI — only how to run a command or fetch a URL, and how
// to parse what comes back.
package collect

import (
	"bytes"
	"context"
	"encoding/json"
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
// parses its output and returns one Observation per key the collector's
// output actually produced this cycle — there's no declared measurement
// list to check it against any more. timeout
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
		return nil, fmt.Errorf("metric %q on %s: %w (output: %s)", def.Name, serverName, err, Excerpt(out))
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("metric %q on %s: collector produced no key=value output", def.Name, serverName)
	}

	return observationsFrom(serverName, def, values), nil
}

// observationsFrom emits one Observation per key in values, unfiltered —
// no declared measurement list exists any more to check a key against,
// so whatever the collector's own output actually produced this cycle
// (executeShell's line-oriented "key=value" output, or executeHTTP's
// auto-discovered top-level JSON numbers plus any Fields entries) simply
// is the set of measurements.
func observationsFrom(serverName string, def *metric.Definition, values map[string]float64) []Observation {
	now := time.Now()
	observations := make([]Observation, 0, len(values))
	for key, val := range values {
		observations = append(observations, Observation{
			Server:    serverName,
			Metric:    def.Name + "." + key,
			Value:     val,
			Timestamp: now,
		})
	}
	return observations
}

// ExecuteObject runs an object definition's shell command once and returns
// its output as compact JSON. Output that isn't valid JSON, or whose
// compact form exceeds the definition's size limit, is an error.
func ExecuteObject(ctx context.Context, runner Runner, def *metric.Definition, timeout time.Duration, config map[string]any) (json.RawMessage, error) {
	command, err := def.Collector.Render(config)
	if err != nil {
		return nil, fmt.Errorf("object %q: %w", def.Name, err)
	}
	out, err := runner.Run(ctx, command, timeout)
	if err != nil {
		return nil, fmt.Errorf("object %q: %w", def.Name, err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(out)); err != nil {
		return nil, fmt.Errorf("object %q: output is not JSON: %w", def.Name, err)
	}
	if limit := def.ObjectLimit(); buf.Len() > limit {
		return nil, fmt.Errorf("object %q: too large (%d bytes > %d)", def.Name, buf.Len(), limit)
	}
	return buf.Bytes(), nil
}
