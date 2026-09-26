// Package endpoint holds the default network location of the Scratchpad
// server, shared by the server and every client (CLI, doctor, MCP bridge) so
// that setting SCRATCHPAD_PORT once keeps them all in agreement.
package endpoint

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPort is the port the server listens on and clients dial when nothing
// else is configured.
const DefaultPort = 8080

// PortEnv names the environment variable that overrides DefaultPort for the
// server and for clients' default URLs.
const PortEnv = "SCRATCHPAD_PORT"

// ParsePort validates a TCP port given as text (1-65535).
func ParsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q: expected a number from 1 to 65535", s)
	}
	return p, nil
}

// Port returns the port from SCRATCHPAD_PORT, or DefaultPort when it is unset.
// An invalid value falls back to DefaultPort; the server validates it strictly
// at startup, so a typo is reported there.
func Port() int {
	if v := os.Getenv(PortEnv); v != "" {
		if p, err := ParsePort(v); err == nil {
			return p
		}
	}
	return DefaultPort
}

// HTTPBase is the default server base URL for HTTP clients.
func HTTPBase() string {
	return fmt.Sprintf("http://localhost:%d", Port())
}

// WSURL is the default WebSocket URL of the web engine.
func WSURL() string {
	return fmt.Sprintf("ws://localhost:%d/ws", Port())
}
