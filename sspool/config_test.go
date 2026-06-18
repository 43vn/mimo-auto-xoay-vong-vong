package sspool

import (
	"encoding/base64"
	"testing"
)

// --- SS parsing tests (existing) ---

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
		Password: "mypass",
		Method:   "aes-256-gcm",
	}
	want := "1.2.3.4:8388:mypass:aes-256-gcm"
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

// --- SOCKS5 parsing tests ---

func TestParseSOCKS5URI(t *testing.T) {
	uri := "socks5://127.0.0.1:1080"
	cfg, err := ParseSOCKS5Server(uri)
	if err != nil {
		t.Fatalf("ParseSOCKS5Server(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "127.0.0.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "127.0.0.1")
	}
	if cfg.Port != 1080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 1080)
	}
	if cfg.Username != "" {
		t.Errorf("Username = %q, want empty", cfg.Username)
	}
}

func TestParseSOCKS5URIWithAuth(t *testing.T) {
	uri := "socks5://user1:pass1@192.168.1.1:1080"
	cfg, err := ParseSOCKS5Server(uri)
	if err != nil {
		t.Fatalf("ParseSOCKS5Server(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "192.168.1.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "192.168.1.1")
	}
	if cfg.Port != 1080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 1080)
	}
	if cfg.Username != "user1" {
		t.Errorf("Username = %q, want %q", cfg.Username, "user1")
	}
	if cfg.Password != "pass1" {
		t.Errorf("Password = %q, want %q", cfg.Password, "pass1")
	}
}

func TestParseSOCKS5URIInvalid(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"no scheme", "127.0.0.1:1080"},
		{"no host", "socks5://:1080"},
		{"bad port", "socks5://127.0.0.1:noport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSOCKS5Server(tc.uri)
			if err == nil {
				t.Errorf("ParseSOCKS5Server(%q) expected error, got nil", tc.uri)
			}
		})
	}
}

// --- HTTP/HTTPS parsing tests ---

func TestParseHTTPURI(t *testing.T) {
	uri := "http://192.168.1.1:8080"
	cfg, err := ParseHTTPServer(uri)
	if err != nil {
		t.Fatalf("ParseHTTPServer(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "192.168.1.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "192.168.1.1")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if cfg.Scheme != "http" {
		t.Errorf("Scheme = %q, want %q", cfg.Scheme, "http")
	}
}

func TestParseHTTPSURI(t *testing.T) {
	uri := "https://proxy.example.com:443"
	cfg, err := ParseHTTPServer(uri)
	if err != nil {
		t.Fatalf("ParseHTTPServer(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "proxy.example.com" {
		t.Errorf("Server = %q, want %q", cfg.Server, "proxy.example.com")
	}
	if cfg.Port != 443 {
		t.Errorf("Port = %d, want %d", cfg.Port, 443)
	}
	if cfg.Scheme != "https" {
		t.Errorf("Scheme = %q, want %q", cfg.Scheme, "https")
	}
}

func TestParseHTTPURIWithAuth(t *testing.T) {
	uri := "http://u1:p1@10.0.0.1:3128"
	cfg, err := ParseHTTPServer(uri)
	if err != nil {
		t.Fatalf("ParseHTTPServer(%q) returned error: %v", uri, err)
	}
	if cfg.Server != "10.0.0.1" {
		t.Errorf("Server = %q, want %q", cfg.Server, "10.0.0.1")
	}
	if cfg.Port != 3128 {
		t.Errorf("Port = %d, want %d", cfg.Port, 3128)
	}
	if cfg.Username != "u1" {
		t.Errorf("Username = %q, want %q", cfg.Username, "u1")
	}
	if cfg.Password != "p1" {
		t.Errorf("Password = %q, want %q", cfg.Password, "p1")
	}
}

// --- ParseProxyURI auto-detect tests ---

func TestParseProxyURIAutoDetect(t *testing.T) {
	tests := []struct {
		name   string
		uri    string
		want   string
	}{
		{"ss", "ss://aes-256-gcm:pass@1.2.3.4:8388", "ss"},
		{"socks5", "socks5://1.2.3.4:1080", "socks5"},
		{"http", "http://1.2.3.4:8080", "http"},
		{"https", "https://1.2.3.4:443", "https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseProxyURI(tc.uri)
			if err != nil {
				t.Fatalf("ParseProxyURI(%q) returned error: %v", tc.uri, err)
			}
			if cfg.Type != tc.want {
				t.Errorf("Type = %q, want %q", cfg.Type, tc.want)
			}
		})
	}
}

func TestParseProxyURIInvalid(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"unknown scheme", "ftp://host:21"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProxyURI(tc.uri)
			if err == nil {
				t.Errorf("ParseProxyURI(%q) expected error, got nil", tc.uri)
			}
		})
	}
}

func TestProxyConfigIsDirect(t *testing.T) {
	if !(ProxyConfig{Type: "direct"}).IsDirect() {
		t.Error("direct type should be IsDirect")
	}
	if !(ProxyConfig{Type: ""}).IsDirect() {
		t.Error("empty type should be IsDirect")
	}
	if (ProxyConfig{Type: "ss"}).IsDirect() {
		t.Error("ss type should not be IsDirect")
	}
}

func TestProxyConfigAddr(t *testing.T) {
	cfg := ProxyConfig{Server: "1.2.3.4", Port: 8388}
	if cfg.Addr() != "1.2.3.4:8388" {
		t.Errorf("Addr() = %q, want %q", cfg.Addr(), "1.2.3.4:8388")
	}
}
