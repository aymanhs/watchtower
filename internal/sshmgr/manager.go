// Package sshmgr manages persistent SSH connections to servers. One
// *ssh.Client is kept per server and reused; individual commands each get
// their own session/channel, so one metric's command never blocks another.
package sshmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/server"
)

// Client is a persistent SSH connection to one server. It dials lazily on
// first use and transparently redials if the connection has gone away, so
// a server flapping never requires restarting the process.
type Client struct {
	cfg *server.Config

	mu   sync.Mutex
	conn *ssh.Client

	// Reconnect backoff, guarded by mu. Every caller on this server shares
	// one gate, so a down host sees one dial per backoff window instead of
	// one per metric per cycle.
	failures    int       // consecutive failed dials
	nextAttempt time.Time // dials before this fail fast
	lastErr     error

	now  func() time.Time // time.Now; overridden by tests
	dial func(c *Client) (*ssh.Client, error)
}

// Backoff tuning: delay ceiling doubles per consecutive failure from
// backoffBase up to backoffCap. A permanent-looking failure (bad
// credentials, changed host key) jumps straight to the cap: retrying fast
// cannot fix it and can trip lockouts such as fail2ban.
const (
	backoffBase = 2 * time.Second
	backoffCap  = 5 * time.Minute
)

// backoffDelay returns how long to wait after the given number of
// consecutive failures. It uses half jitter (ceiling/2 plus a random share
// of the other half): it spreads out servers that failed together while
// still guaranteeing real spacing between attempts. rnd is in [0, 1).
func backoffDelay(failures int, permanent bool, rnd float64) time.Duration {
	ceiling := backoffCap
	if !permanent && failures > 0 {
		shift := failures - 1
		if shift > 20 {
			shift = 20
		}
		if d := backoffBase << shift; d < ceiling {
			ceiling = d
		}
	}
	half := ceiling / 2
	return half + time.Duration(rnd*float64(half))
}

// isPermanent reports whether err is a failure that retrying quickly
// cannot fix: rejected credentials, a changed/unknown host key, or local
// auth/host-key setup problems.
func isPermanent(err error) bool {
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "auth setup") ||
		strings.Contains(msg, "host key verification setup")
}

// Manager owns one Client per server, keyed by server name.
type Manager struct {
	mu      sync.Mutex
	clients map[string]*Client
}

func NewManager() *Manager {
	return &Manager{clients: make(map[string]*Client)}
}

// Client returns the persistent client for cfg, creating it if necessary.
// It does not dial; dialing happens lazily inside Run so start-up never
// blocks on a slow or unreachable server.
func (m *Manager) Client(cfg *server.Config) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[cfg.Name]; ok {
		return c
	}
	c := &Client{cfg: cfg, now: time.Now, dial: (*Client).dialSSH}
	m.clients[cfg.Name] = c
	return c
}

// Close drops the connection, if any. The client stays usable: the next Run
// dials again.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Connect establishes (or verifies) the persistent connection up front.
// Callers should call this once at start-up for every configured server,
// concurrently and independently, so that a metric's own timeout never has
// to also pay for a cold SSH handshake on its first execution. Run still
// connects lazily if Connect was never called or the connection dropped.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensureConnectedLocked()
}

// Run executes command on a new session/channel over the persistent
// connection, bounded by timeout. Stdout is returned on success. A
// non-zero exit, a transport error or a timeout is returned as an error —
// never as an empty or zero result, so a collection failure can't be
// mistaken for a measurement.
func (c *Client) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	if err := c.ensureConnectedLocked(); err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("connect: %w", err)
	}
	conn := c.conn
	c.mu.Unlock()

	session, err := conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("open session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := session.Start(command); err != nil {
		return "", fmt.Errorf("start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("command exited with error: %w (stderr: %s)", err, collect.Excerpt(stderr.String()))
		}
		return stdout.String(), nil
	case <-runCtx.Done():
		session.Close() // closing the channel terminates the remote process on virtually all sshd configs
		return "", fmt.Errorf("command timed out after %s", timeout)
	}
}

// DialContext opens a TCP connection tunneled through this SSH session —
// so "localhost:8222" reaches that port as the remote server sees it, not
// as this machine does. Satisfies collect.Dialer, for a CollectorHTTP
// definition to fetch a loopback-bound endpoint on an ssh-type server
// exactly as if it were running there itself, no remote shell needed.
//
// golang.org/x/crypto/ssh.Client.Dial has no context parameter; ctx
// cancellation is applied here by racing it against the dial.
func (c *Client) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.mu.Lock()
	if err := c.ensureConnectedLocked(); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}
	conn := c.conn
	c.mu.Unlock()

	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		nc, err := conn.Dial(network, address)
		ch <- result{nc, err}
	}()

	select {
	case r := <-ch:
		return r.conn, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// OpenShell starts an interactive login shell on a new session/channel —
// a genuine remote terminal, not a one-shot command like Run. The
// returned shellConn is an io.ReadWriteCloser (what you write goes to the
// shell's stdin, what you read is its stdout+stderr) that also has a
// Resize(cols, rows int) error method; a terminal view that type-asserts
// for it (as github.com/egoist/mygo/plugins/terminal's Conn option does)
// can keep the remote PTY's size matching the window's as it changes.
func (c *Client) OpenShell(cols, rows int) (*shellConn, error) {
	c.mu.Lock()
	// User-initiated: ignore the backoff gate once (the outcome still
	// feeds the failure count).
	if err := c.connectLocked(true); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}
	conn := c.conn
	c.mu.Unlock()

	session, err := conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}

	modes := ssh.TerminalModes{ssh.ECHO: 1}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := session.Shell(); err != nil {
		session.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	return &shellConn{session: session, stdin: stdin, stdout: stdout}, nil
}

// shellConn adapts an *ssh.Session running an interactive shell to
// io.ReadWriteCloser plus the Resize method a terminal view looks for.
type shellConn struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

func (s *shellConn) Read(p []byte) (int, error)  { return s.stdout.Read(p) }
func (s *shellConn) Write(p []byte) (int, error) { return s.stdin.Write(p) }
func (s *shellConn) Close() error                { return s.session.Close() }

// Resize tells the remote PTY its new size. cols-then-rows matches what
// github.com/egoist/mygo/plugins/terminal's Options.Conn doc specifies;
// ssh.Session.WindowChange takes height then width, the other order.
func (s *shellConn) Resize(cols, rows int) error {
	return s.session.WindowChange(rows, cols)
}

// ensureConnectedLocked returns nil if a live connection exists, dialing
// if needed unless the backoff gate says to wait. Caller must hold c.mu.
func (c *Client) ensureConnectedLocked() error { return c.connectLocked(false) }

func (c *Client) connectLocked(force bool) error {
	if c.conn != nil {
		// Cheap liveness probe: any global request round-trip proves the
		// transport is alive, whether or not the server understands it.
		if _, _, err := c.conn.SendRequest("keepalive@watchtower", true, nil); err == nil {
			return nil
		}
		c.conn.Close()
		c.conn = nil
	}

	if wait := c.nextAttempt.Sub(c.now()); !force && wait > 0 {
		return fmt.Errorf("backing off, next attempt in %s: %w", wait.Round(time.Second), c.lastErr)
	}

	conn, err := c.dial(c)
	if err != nil {
		c.failures++
		c.lastErr = err
		c.nextAttempt = c.now().Add(backoffDelay(c.failures, isPermanent(err), rand.Float64()))
		return err
	}
	c.conn = conn
	c.failures = 0
	c.lastErr = nil
	c.nextAttempt = time.Time{}
	return nil
}

// dialSSH performs one real dial attempt.
func (c *Client) dialSSH() (*ssh.Client, error) {
	auth, err := authMethods(c.cfg.Connection.Key)
	if err != nil {
		return nil, fmt.Errorf("auth setup: %w", err)
	}
	hostKeyCB, err := hostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("host key verification setup: %w", err)
	}

	addr := net.JoinHostPort(c.cfg.Connection.Host, fmt.Sprintf("%d", c.cfg.Connection.Port))
	clientCfg := &ssh.ClientConfig{
		User:            c.cfg.Connection.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCB,
		Timeout:         10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, nil
}

// authMethods prefers a running ssh-agent (handles passphrase-protected
// keys) and falls back to reading keyPath directly.
func authMethods(keyPath string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ag := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(ag.Signers))
		}
	}

	if keyPath != "" {
		expanded, err := expandHome(keyPath)
		if err != nil {
			return nil, err
		}
		keyBytes, readErr := os.ReadFile(expanded)
		if readErr != nil {
			if len(methods) > 0 {
				return methods, nil // agent may still work without this key
			}
			return nil, fmt.Errorf("read key %s: %w", expanded, readErr)
		}
		signer, parseErr := ssh.ParsePrivateKey(keyBytes)
		if parseErr != nil {
			if len(methods) > 0 {
				return methods, nil
			}
			return nil, fmt.Errorf("parse key %s: %w (passphrase-protected keys need ssh-agent)", expanded, parseErr)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no usable auth method: no ssh-agent (SSH_AUTH_SOCK unset) and no usable key at %q", keyPath)
	}
	return methods, nil
}

// hostKeyCallback verifies server identity against the user's known_hosts,
// per the brief's host-key verification requirement. An unknown or changed
// host key is rejected, not silently accepted.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s: %w", path, err)
	}
	return cb, nil
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}
