// Package localrun runs collector commands and dials collector connections
// directly on the machine running Watchtower — no SSH hop. It exists for
// endpoints reachable straight from wherever the app runs, where hopping
// through a remote server's SSH session would be pointless. It satisfies
// collect.Backend (both Runner and Dialer), the exact same interface
// sshmgr.Client does, so internal/app wires it in without either package
// knowing the other exists.
//
// Collector commands are written in POSIX shell throughout this project
// (awk, grep, curl, vmstat, ...) on the assumption they run on a Linux
// target over SSH. Runner executes them the same way — via "sh -c" —
// which means a "local" server config only works where "sh" exists on
// the machine running Watchtower itself: Linux, macOS, WSL, or Windows
// with a POSIX shell on PATH (e.g. Git Bash). A plain Windows install
// has no such shell; "local" servers there will fail with a clear
// "executable file not found" error, not silently produce nothing.
package localrun

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"time"

	"aymanhs/watchtower/internal/collect"
)

// Runner runs commands via "sh -c" on the local machine, bounded by
// timeout. Stateless and safe for concurrent use — unlike sshmgr.Client
// there's no persistent connection to hold.
type Runner struct{}

// New returns a Runner. There is no configuration: it always runs on this
// machine, as whichever user Watchtower itself runs as.
func New() *Runner { return &Runner{} }

// Run executes command, returning stdout on success. A non-zero exit or a
// timeout is returned as an error — never as an empty or zero result, so
// a collection failure can't be mistaken for a measurement, the same
// contract sshmgr.Client.Run makes.
func (r *Runner) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		return "", fmt.Errorf("command exited with error: %w (stderr: %s)", err, collect.Excerpt(stderr.String()))
	}
	return stdout.String(), nil
}

// DialContext opens a TCP connection directly from this machine — no
// tunnel needed, it's already wherever Watchtower runs. Satisfies
// collect.Dialer, for a CollectorHTTP definition on a "local" server.
func (r *Runner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
