package middleware

import (
	"testing"
	"time"
)

func TestCurrentWindowKey(t *testing.T) {
	// 2026-05-22T14:23:15Z floors to the minute → "202605221423".
	at := time.Date(2026, 5, 22, 14, 23, 15, 0, time.UTC)
	got := currentWindowKey("rl", "tn_x", at)
	want := "rl:tn_x:202605221423"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNextWindowUnixIsNextMinute(t *testing.T) {
	at := time.Date(2026, 5, 22, 14, 23, 15, 0, time.UTC)
	got := nextWindowUnix(at)
	want := time.Date(2026, 5, 22, 14, 24, 0, 0, time.UTC).Unix()
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestNextWindowUnixOnMinuteBoundary(t *testing.T) {
	// Exactly on the minute → still NEXT minute (the current second's
	// window has just opened).
	at := time.Date(2026, 5, 22, 14, 23, 0, 0, time.UTC)
	got := nextWindowUnix(at)
	want := time.Date(2026, 5, 22, 14, 24, 0, 0, time.UTC).Unix()
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
