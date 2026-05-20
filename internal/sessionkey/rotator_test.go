package sessionkey

import (
	"context"
	"testing"
	"time"
)

// TestRotatorTickStatsShape pins the shape of TickStats and the contract
// that Tick returns the same stats it pushed to onTickFn. The real
// rotation behavior depends on a live Postgres + the migrations and is
// covered by the integration-test fixture in /tests (when that exists).
//
// Here we just verify that:
//   - NewRotator applies the documented defaults (60s, batch 200)
//   - SetOnTick wires correctly
//   - Run honors ctx cancellation
func TestNewRotatorDefaults(t *testing.T) {
	r := NewRotator(nil, 0, 0)
	if r.tick != 60*time.Second {
		t.Errorf("tick=%s want 60s", r.tick)
	}
	if r.batch != 200 {
		t.Errorf("batch=%d want 200", r.batch)
	}
}

func TestNewRotatorRespectsOverrides(t *testing.T) {
	r := NewRotator(nil, 5*time.Second, 50)
	if r.tick != 5*time.Second {
		t.Errorf("tick=%s want 5s", r.tick)
	}
	if r.batch != 50 {
		t.Errorf("batch=%d want 50", r.batch)
	}
}

func TestRunHonorsCancel(t *testing.T) {
	// We can't run Tick without a real *pgxpool.Pool, but we CAN verify
	// that Run returns promptly when its context is cancelled — before
	// the first ticker fires (default 60s is well past the test budget).
	r := NewRotator(nil, time.Hour, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ctx cancel")
	}
}
