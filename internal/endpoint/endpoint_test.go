package endpoint

import "testing"

func TestParsePort(t *testing.T) {
	for _, s := range []string{"1", "8080", "65535", " 9000 "} {
		if _, err := ParsePort(s); err != nil {
			t.Errorf("ParsePort(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "0", "65536", "-1", "http", "80a"} {
		if _, err := ParsePort(s); err == nil {
			t.Errorf("ParsePort(%q) accepted an invalid port", s)
		}
	}
}

func TestDefaultsFollowPortEnv(t *testing.T) {
	t.Setenv(PortEnv, "")
	if HTTPBase() != "http://localhost:8080" || WSURL() != "ws://localhost:8080/ws" {
		t.Errorf("defaults = %s, %s", HTTPBase(), WSURL())
	}
	t.Setenv(PortEnv, "9123")
	if HTTPBase() != "http://localhost:9123" || WSURL() != "ws://localhost:9123/ws" {
		t.Errorf("with %s=9123: %s, %s", PortEnv, HTTPBase(), WSURL())
	}
	t.Setenv(PortEnv, "not-a-port")
	if Port() != DefaultPort {
		t.Errorf("invalid %s should fall back to %d, got %d", PortEnv, DefaultPort, Port())
	}
}
