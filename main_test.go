package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRoutesServeBasePathAndRoot(t *testing.T) {
	app := &appServer{cfg: config{
		basePath:  "/app/fnssh",
		localHost: "127.0.0.1",
		localPort: 22,
	}}
	handler := app.routes(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("index ok")},
		"app.js":     &fstest.MapFile{Data: []byte("console.log('ok');")},
	})

	tests := []struct {
		path       string
		statusCode int
		contains   string
	}{
		{path: "/app/fnssh/", statusCode: http.StatusOK, contains: "index ok"},
		{path: "/app/fnssh/app.js", statusCode: http.StatusOK, contains: "console.log"},
		{path: "/app/fnssh/api/config", statusCode: http.StatusOK, contains: `"localHost":"127.0.0.1"`},
		{path: "/api/config", statusCode: http.StatusOK, contains: `"localHost":"127.0.0.1"`},
		{path: "/app/fnssh", statusCode: http.StatusTemporaryRedirect, contains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.statusCode, rec.Body.String())
			}
			if tt.contains != "" && !strings.Contains(rec.Body.String(), tt.contains) {
				t.Fatalf("body %q does not contain %q", rec.Body.String(), tt.contains)
			}
		})
	}
}

func TestCheckOriginAllowsGatewayForwardedHost(t *testing.T) {
	app := &appServer{}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/ws", nil)
	req.Header.Set("Origin", "https://fn.example:5666")
	req.Header.Set("X-Forwarded-Host", "fn.example:5666")

	if !app.checkOrigin(req) {
		t.Fatal("expected forwarded host origin to be allowed")
	}
}

func TestCheckOriginRejectsUnrelatedHost(t *testing.T) {
	app := &appServer{}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/ws", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-Forwarded-Host", "fn.example:5666")

	if app.checkOrigin(req) {
		t.Fatal("expected unrelated origin to be rejected")
	}
}
