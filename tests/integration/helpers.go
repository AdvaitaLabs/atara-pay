//go:build integration

package integration

import (
	"net"
	"testing"
	"time"

	"github.com/atara-xyz/atara-pay/internal/server"
)

// startEphemeral binds the server to 127.0.0.1:<random-free-port> in a
// goroutine and returns the host:port. Fiber's Listen blocks, so we ask
// the kernel for a free port, hand it to Fiber, then wait a few ms for
// the listener to be ready. Cleanup is "let the goroutine die when the
// test ends" — fine because the OS reclaims sockets on process exit.
func startEphemeral(t *testing.T, s *server.Server) string {
	t.Helper()

	// Ask the kernel for a free port by binding briefly.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	go func() {
		_ = s.Listen(addr)
	}()

	// Wait until the server is actually accepting connections. 50ms is
	// usually enough; we retry for ~2s as a belt.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return addr
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server did not start on %s within 2s", addr)
	return ""
}
