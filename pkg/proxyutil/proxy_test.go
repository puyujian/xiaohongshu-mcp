package proxyutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePoolResponse(t *testing.T) {
	got, err := ParsePoolResponse("218.93.164.126:58861\r\n")
	if err != nil {
		t.Fatalf("ParsePoolResponse 返回错误: %v", err)
	}
	if got != "218.93.164.126:58861" {
		t.Fatalf("ParsePoolResponse = %q, want %q", got, "218.93.164.126:58861")
	}
}

func TestResolveUsesPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://127.0.0.1:8899"))
	}))
	defer srv.Close()

	got, err := Resolve(context.Background(), "", srv.URL)
	if err != nil {
		t.Fatalf("Resolve 返回错误: %v", err)
	}
	if got != "http://127.0.0.1:8899" {
		t.Fatalf("Resolve = %q, want %q", got, "http://127.0.0.1:8899")
	}
}

func TestNormalizeHTTPProxy(t *testing.T) {
	u, err := NormalizeHTTPProxy("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("NormalizeHTTPProxy 返回错误: %v", err)
	}
	if got := u.String(); got != "http://127.0.0.1:8080" {
		t.Fatalf("NormalizeHTTPProxy = %q, want %q", got, "http://127.0.0.1:8080")
	}
}
