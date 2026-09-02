package proxy

import "testing"

func TestParseSupportedFormats(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		host   string
		port   int
		user   string
		pass   string
		scheme string
	}{
		{name: "url no auth", input: "socks5://1.2.3.4:1080", host: "1.2.3.4", port: 1080, scheme: "socks5"},
		{name: "url auth", input: "socks5://user:pass@example.com:1080", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5"},
		{name: "url socks5h", input: "socks5h://user:pass@example.com:1080", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5h"},
		{name: "url http", input: "http://user:pass@example.com:8080", host: "example.com", port: 8080, user: "user", pass: "pass", scheme: "http"},
		{name: "url https", input: "https://example.com:8443", host: "example.com", port: 8443, scheme: "https"},
		{name: "user pass at host", input: "user:pass@example.com:1080", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5"},
		{name: "host at user pass", input: "example.com:1080@user:pass", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5"},
		{name: "host port user pass", input: "example.com:1080:user:pass", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5"},
		{name: "user pass host port", input: "user:pass:example.com:1080", host: "example.com", port: 1080, user: "user", pass: "pass", scheme: "socks5"},
		{name: "host port", input: "example.com:1080", host: "example.com", port: 1080, scheme: "socks5"},
		{name: "ipv6", input: "[2001:db8::1]:1080", host: "2001:db8::1", port: 1080, scheme: "socks5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.Host != tt.host || got.Port != tt.port || got.User != tt.user || got.Pass != tt.pass || got.Scheme != tt.scheme {
				t.Fatalf("Parse() = %#v", got)
			}
			if got.Raw != tt.input {
				t.Fatalf("Raw = %q, want %q", got.Raw, tt.input)
			}
		})
	}
}

func TestParseRejectsAmbiguousFourPartFormat(t *testing.T) {
	_, err := Parse("host:1080:user:2048")
	if err == nil {
		t.Fatal("expected ambiguous format error")
	}
}

func TestParseLinesKeepsValidRows(t *testing.T) {
	proxies, errors := ParseLines("example.com:1080\nbad-line\nuser:pass@example.net:1081")
	if len(proxies) != 2 {
		t.Fatalf("len(proxies) = %d, want 2", len(proxies))
	}
	if len(errors) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(errors))
	}
	if errors[0].Line != 2 {
		t.Fatalf("error line = %d, want 2", errors[0].Line)
	}
}

func TestDedupKeyTreatsSocks5AndSocks5HAsDifferentProxies(t *testing.T) {
	a := Proxy{Scheme: "socks5", Host: "example.com", Port: 1080, User: "user", Pass: "pass"}
	b := Proxy{Scheme: "socks5h", Host: "example.com", Port: 1080, User: "user", Pass: "pass"}
	if a.DedupKey() == b.DedupKey() {
		t.Fatalf("DedupKey() treated socks5 and socks5h as the same proxy: %q", a.DedupKey())
	}
}
