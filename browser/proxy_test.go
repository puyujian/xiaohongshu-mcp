package browser

import "testing"

func TestNormalizeProxy(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty", in: "", want: "", wantErr: false},
		{name: "hostPort", in: "127.0.0.1:7890", want: "127.0.0.1:7890", wantErr: false},
		{name: "httpURL", in: "http://127.0.0.1:7890", want: "127.0.0.1:7890", wantErr: false},
		{name: "httpsURL", in: "https://127.0.0.1:7890", want: "127.0.0.1:7890", wantErr: false},
		{name: "httpURLWithUserInfo", in: "http://user:pass@127.0.0.1:7890", want: "127.0.0.1:7890", wantErr: false},
		{name: "socks5URL", in: "socks5://127.0.0.1:1080", want: "socks5://127.0.0.1:1080", wantErr: false},
		{name: "socks5hURL", in: "socks5h://127.0.0.1:1080", want: "socks5://127.0.0.1:1080", wantErr: false},
		{name: "socksURL", in: "socks://127.0.0.1:1080", want: "socks5://127.0.0.1:1080", wantErr: false},
		{name: "invalid", in: "not-a-proxy", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeProxy(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeProxy(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeProxyForLog(t *testing.T) {
	got := sanitizeProxyForLog("http://user:pass@127.0.0.1:7890")
	if got != "http://***@127.0.0.1:7890" {
		t.Fatalf("sanitizeProxyForLog: got %q", got)
	}
}

func TestParseProxyAuth(t *testing.T) {
	auth, err := parseProxyAuth("http://user:pass@127.0.0.1:2323")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth == nil || auth.Username != "user" || auth.Password != "pass" {
		t.Fatalf("unexpected auth: %+v", auth)
	}

	auth, err = parseProxyAuth("user:pass@127.0.0.1:2323")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth == nil || auth.Username != "user" || auth.Password != "pass" {
		t.Fatalf("unexpected auth: %+v", auth)
	}

	auth, err = parseProxyAuth("127.0.0.1:2323")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth != nil {
		t.Fatalf("expected nil auth, got %+v", auth)
	}
}
