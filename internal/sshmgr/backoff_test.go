package sshmgr

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"aymanhs/watchtower/internal/server"
)

func TestBackoffDelayGrowsAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for f := 1; f <= 5; f++ {
		d := backoffDelay(f, false, 0) // lower bound of the jitter range
		if d < prev {
			t.Fatalf("delay shrank at failure %d: %s < %s", f, d, prev)
		}
		prev = d
	}
	if got := backoffDelay(1, false, 0); got != backoffBase/2 {
		t.Errorf("first lower bound = %s, want %s", got, backoffBase/2)
	}
	if got := backoffDelay(100, false, 0.999999); got > backoffCap || got < backoffCap/2 {
		t.Errorf("capped delay = %s, want within [%s, %s]", got, backoffCap/2, backoffCap)
	}
	if got := backoffDelay(1, true, 0); got != backoffCap/2 {
		t.Errorf("permanent failure delay = %s, want %s", got, backoffCap/2)
	}
}

func TestIsPermanent(t *testing.T) {
	if !isPermanent(errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none]")) {
		t.Error("auth rejection should be permanent")
	}
	if !isPermanent(&knownhosts.KeyError{}) {
		t.Error("host key mismatch should be permanent")
	}
	if isPermanent(errors.New("dial tcp 1.2.3.4:22: i/o timeout")) {
		t.Error("network timeout should not be permanent")
	}
}

func newTestClient(now *time.Time, dials *int, fail *bool) *Client {
	c := &Client{cfg: &server.Config{Name: "x"}, now: func() time.Time { return *now }}
	c.dial = func(*Client) (*ssh.Client, error) {
		*dials++
		if *fail {
			return nil, errors.New("dial tcp: i/o timeout")
		}
		return nil, errors.New("unreachable in this test")
	}
	return c
}

func TestFailedDialsShareOneBackoffGate(t *testing.T) {
	now := time.Now()
	dials, fail := 0, true
	c := newTestClient(&now, &dials, &fail)

	if err := c.ensureConnectedLocked(); err == nil {
		t.Fatal("expected dial error")
	}
	// Five more metrics on the same host in the same window: no new dials.
	for i := 0; i < 5; i++ {
		err := c.ensureConnectedLocked()
		if err == nil || !strings.Contains(err.Error(), "backing off") {
			t.Fatalf("call %d: want backoff error, got %v", i, err)
		}
	}
	if dials != 1 {
		t.Fatalf("dials = %d, want 1", dials)
	}

	// Past the window, exactly one more attempt is allowed.
	now = now.Add(backoffBase + time.Second)
	c.ensureConnectedLocked()
	if dials != 2 {
		t.Fatalf("dials after window = %d, want 2", dials)
	}
	if c.failures != 2 {
		t.Fatalf("failures = %d, want 2", c.failures)
	}
}

func TestForceBypassesGate(t *testing.T) {
	now := time.Now()
	dials, fail := 0, true
	c := newTestClient(&now, &dials, &fail)
	c.ensureConnectedLocked()
	c.connectLocked(true)
	if dials != 2 {
		t.Fatalf("dials = %d, want 2 (forced attempt ignores the gate)", dials)
	}
}
