package main

import (
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
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

func TestWebSocketKeepaliveAndCleanup(t *testing.T) {
	sessions := make(chan *wsSession, 1)
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		session := &wsSession{conn: conn, done: make(chan struct{})}
		sessions <- session
		session.keepWebSocketAlive(5 * time.Millisecond)
		close(stopped)
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	session := <-sessions
	defer session.close()
	ping := make(chan struct{}, 1)
	conn.SetPingHandler(func(string) error {
		select {
		case ping <- struct{}{}:
		default:
		}
		return nil
	})
	readDone := make(chan struct{})
	go func() { defer close(readDone); _, _, _ = conn.ReadMessage() }()
	select {
	case <-ping:
	case <-time.After(time.Second):
		t.Fatal("no keepalive ping received")
	}
	session.close()
	session.close() // Cleanup is safe when multiple goroutines detect disconnect.
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not stop after close")
	}
	_ = conn.Close()
	<-readDone
}

func TestWebSocketMissingPongClosesSession(t *testing.T) {
	sessions := make(chan *wsSession, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		session := &wsSession{conn: conn, done: make(chan struct{})}
		defer session.close()
		session.startWebSocketKeepalive(5*time.Millisecond, 50*time.Millisecond)
		sessions <- session
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	session := <-sessions
	// Deliberately do not read: no pong will be sent, as with a lost network.
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("unresponsive browser session was not closed")
	}
}
