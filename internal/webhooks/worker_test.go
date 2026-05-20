package webhooks

import (
	"testing"
	"time"
)

func TestBackoffMonotonic(t *testing.T) {
	// Boundary checks: 30s → 2m → 10m → 1h → 6h → 24h.
	want := []time.Duration{
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		1 * time.Hour,
		6 * time.Hour,
		24 * time.Hour,
		24 * time.Hour, // cap
		24 * time.Hour, // cap
	}
	for i, w := range want {
		got := backoffFor(i + 1)
		if got != w {
			t.Errorf("backoffFor(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestIntToString(t *testing.T) {
	cases := []struct {
		in  int
		out string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{1000, "1000"},
		{-7, "-7"},
	}
	for _, c := range cases {
		if got := intToString(c.in); got != c.out {
			t.Errorf("intToString(%d) = %q, want %q", c.in, got, c.out)
		}
	}
}
