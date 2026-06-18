package sspool

import (
	"encoding/base64"
	"testing"
)

func TestParseSSServerStandard(t *testing.T) {
	uri := "ss://aes-256-gcm:mypassword@192.168.1.1:8388"
	cfg, err := ParseSSServer(uri)
	if err != nil {
		t.Fatalf("ParseSSServer(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "192.168.1.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "192.168.1.1")
	}
	if cfg.Port != 8388 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8388)
	}
	if cfg.Password != "mypassword" {
		t.Errorf("Password = %q, want %q", cfg.Password, "mypassword")
	}
	if cfg.Method != "aes-256-gcm" {
		t.Errorf("Method = %q, want %q", cfg.Method, "aes-256-gcm")
	}
}

func TestParseSSServerBase64(t *testing.T) {
	// Base64-encode "aes-256-gcm:pass123" for userinfo
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass123"))
	uri := "ss://" + userinfo + "@10.0.0.1:443"
	cfg, err := ParseSSServer(uri)
	if err != nil {
		t.Fatalf("ParseSSServer(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "10.0.0.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "10.0.0.1")
	}
	if cfg.Port != 443 {
		t.Errorf("Port = %d, want %d", cfg.Port, 443)
	}
	if cfg.Password != "pass123" {
		t.Errorf("Password = %q, want %q", cfg.Password, "pass123")
	}
	if cfg.Method != "aes-256-gcm" {
		t.Errorf("Method = %q, want %q", cfg.Method, "aes-256-gcm")
	}
}

func TestParseSSServerInvalid(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"no scheme", "aes-256-gcm:pass@host:8388"},
		{"no host", "ss://aes-256-gcm:pass@:8388"},
		{"no port", "ss://aes-256-gcm:pass@host"},
		{"bad port", "ss://aes-256-gcm:pass@host:notaport"},
		{"no userinfo", "ss://host:8388"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSSServer(tc.uri)
			if err == nil {
				t.Errorf("ParseSSServer(%q) expected error, got nil", tc.uri)
			}
		})
	}
}

func TestSSConfigKey(t *testing.T) {
	cfg := SSConfig{
		Server:   "1.2.3.4",
		Port:     8388,
		Password: "secret",
		Method:   "aes-256-gcm",
	}
	want := "1.2.3.4:8388:secret:aes-256-gcm"
	if got := cfg.Key(); got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

func TestSSConfigKeyDifferentPassword(t *testing.T) {
	c1 := SSConfig{Server: "1.2.3.4", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	c2 := SSConfig{Server: "1.2.3.4", Port: 8388, Password: "b", Method: "aes-256-gcm"}
	if c1.Key() == c2.Key() {
		t.Errorf("different passwords should produce different keys, both got %q", c1.Key())
	}
}
