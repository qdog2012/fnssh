package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

//go:embed web
var staticFS embed.FS

type config struct {
	addr         string
	basePath     string
	unixSocket   string
	localHost    string
	localPort    int
	token        string
	allowedHosts map[string]struct{}
	allowOrigin  string
	dialTimeout  time.Duration
	knownHosts   string
}

type apiConfig struct {
	LocalHost     string `json:"localHost"`
	LocalPort     int    `json:"localPort"`
	RequiresToken bool   `json:"requiresToken"`
}

type clientMessage struct {
	Type       string `json:"type"`
	Mode       string `json:"mode,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
	Data       string `json:"data,omitempty"`
}

type serverMessage struct {
	Type    string `json:"type"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
	Data    string `json:"data,omitempty"`
}

type appServer struct {
	cfg      config
	upgrader websocket.Upgrader
}

func main() {
	cfg := loadConfig()
	staticRoot, err := fs.Sub(staticFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	app := &appServer{cfg: cfg}
	app.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     app.checkOrigin,
	}

	handler := app.routes(staticRoot)

	if err := serve(cfg, logRequests(handler)); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() config {
	return config{
		addr:         envString("FNSSH_ADDR", ":5123"),
		basePath:     normalizeBasePath(os.Getenv("FNSSH_BASE_PATH")),
		unixSocket:   strings.TrimSpace(os.Getenv("FNSSH_UNIX_SOCKET")),
		localHost:    envString("FNSSH_LOCAL_HOST", "127.0.0.1"),
		localPort:    envInt("FNSSH_LOCAL_PORT", 22),
		token:        strings.TrimSpace(os.Getenv("FNSSH_TOKEN")),
		allowedHosts: envSet("FNSSH_ALLOWED_HOSTS"),
		allowOrigin:  strings.TrimSpace(os.Getenv("FNSSH_ALLOW_ORIGIN")),
		dialTimeout:  time.Duration(envInt("FNSSH_DIAL_TIMEOUT_SECONDS", 15)) * time.Second,
		knownHosts:   strings.TrimSpace(os.Getenv("FNSSH_KNOWN_HOSTS")),
	}
}

func (s *appServer) routes(staticRoot fs.FS) http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/api/config", s.handleConfig)
	appMux.HandleFunc("/ws", s.handleWS)
	appMux.Handle("/", http.FileServer(http.FS(staticRoot)))

	if s.cfg.basePath == "" {
		return appMux
	}

	rootMux := http.NewServeMux()
	rootMux.Handle(s.cfg.basePath+"/", http.StripPrefix(s.cfg.basePath, appMux))
	rootMux.Handle(s.cfg.basePath, http.RedirectHandler(s.cfg.basePath+"/", http.StatusTemporaryRedirect))
	rootMux.Handle("/", appMux)
	return rootMux
}

func normalizeBasePath(value string) string {
	value = "/" + strings.Trim(strings.TrimSpace(value), "/")
	if value == "/" {
		return ""
	}
	return value
}

func serve(cfg config, handler http.Handler) error {
	errCh := make(chan error, 2)

	tcpServer := &http.Server{Addr: cfg.addr, Handler: handler}
	go func() {
		log.Printf("fnssh listening on tcp %s", cfg.addr)
		errCh <- tcpServer.ListenAndServe()
	}()

	if cfg.unixSocket != "" {
		listener, err := listenUnix(cfg.unixSocket)
		if err != nil {
			return err
		}
		unixServer := &http.Server{Handler: handler}
		go func() {
			log.Printf("fnssh listening on unix %s", cfg.unixSocket)
			errCh <- unixServer.Serve(listener)
		}()
	}

	return <-errCh
}

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(unixPathDir(socketPath), 0755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	if info, err := os.Stat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refuse to replace non-socket file %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0666); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("chmod unix socket: %w", err)
	}
	return listener, nil
}

func unixPathDir(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envSet(key string) map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			set[item] = struct{}{}
		}
	}
	return set
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (s *appServer) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, apiConfig{
		LocalHost:     s.cfg.localHost,
		LocalPort:     s.cfg.localPort,
		RequiresToken: s.cfg.token != "",
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write json: %v", err)
	}
}

func (s *appServer) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade websocket: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(4 << 20)

	var first clientMessage
	if err := conn.ReadJSON(&first); err != nil {
		log.Printf("read connect message: %v", err)
		return
	}
	if first.Type != "connect" {
		_ = conn.WriteJSON(serverMessage{Type: "error", Message: "first message must be connect"})
		return
	}

	ctx := &wsSession{conn: conn}
	if err := ctx.collectInteractiveCredentials(&first); err != nil {
		ctx.send(serverMessage{Type: "error", Message: err.Error()})
		return
	}
	if err := s.connectSSH(ctx, first); err != nil {
		ctx.send(serverMessage{Type: "error", Message: err.Error()})
		return
	}
	defer ctx.close()

	ctx.send(serverMessage{Type: "status", State: "connected", Message: "SSH session connected"})
	go ctx.copyOutput(ctx.stdout)
	go ctx.copyOutput(ctx.stderr)
	go ctx.wait()

	for {
		var msg clientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "input":
			if msg.Data != "" {
				if _, err := io.WriteString(ctx.stdin, msg.Data); err != nil {
					ctx.send(serverMessage{Type: "error", Message: fmt.Sprintf("write stdin: %v", err)})
					return
				}
			}
		case "resize":
			cols, rows := saneSize(msg.Cols, msg.Rows)
			if err := ctx.session.WindowChange(rows, cols); err != nil {
				ctx.send(serverMessage{Type: "error", Message: fmt.Sprintf("resize failed: %v", err)})
			}
		case "close":
			return
		}
	}
}

func (s *appServer) authorized(r *http.Request) bool {
	if s.cfg.token == "" {
		return true
	}
	got := r.URL.Query().Get("token")
	if got == "" {
		got = r.Header.Get("X-FNSSH-Token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.token)) == 1
}

func (s *appServer) checkOrigin(r *http.Request) bool {
	if s.cfg.allowOrigin == "*" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if s.cfg.allowOrigin != "" {
		return strings.EqualFold(parsed.Host, s.cfg.allowOrigin)
	}
	return sameHost(parsed.Host, r.Host, r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Server"))
}

func sameHost(originHost string, candidates ...string) bool {
	originHost = normalizeHost(originHost)
	for _, candidate := range candidates {
		if normalizeHost(candidate) == originHost {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	return strings.ToLower(host)
}

func (s *appServer) connectSSH(ctx *wsSession, req clientMessage) error {
	host, port, err := s.resolveTarget(req)
	if err != nil {
		return err
	}
	if !s.hostAllowed(host) {
		return fmt.Errorf("host %q is not allowed by FNSSH_ALLOWED_HOSTS", host)
	}

	auth, err := authMethods(req)
	if err != nil {
		return err
	}
	hostKeyCallback, err := s.hostKeyCallback()
	if err != nil {
		return err
	}

	sshConfig := &ssh.ClientConfig{
		User:            strings.TrimSpace(req.Username),
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         s.cfg.dialTimeout,
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return fmt.Errorf("dial %s: %w", address, err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("new ssh session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	cols, rows := saneSize(req.Cols, req.Rows)
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("request pty: %w", err)
	}
	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	ctx.client = client
	ctx.session = session
	ctx.stdin = stdin
	ctx.stdout = stdout
	ctx.stderr = stderr
	return nil
}

func (s *appServer) resolveTarget(req clientMessage) (string, int, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "local" && strings.TrimSpace(req.Host) == "" {
		return s.cfg.localHost, s.cfg.localPort, nil
	}
	if mode != "" && mode != "remote" && mode != "local" {
		return "", 0, fmt.Errorf("unsupported mode %q", req.Mode)
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = s.cfg.localHost
	}
	port := req.Port
	if port <= 0 || port > 65535 {
		port = 22
	}
	return host, port, nil
}

func (s *appServer) hostAllowed(host string) bool {
	if len(s.cfg.allowedHosts) == 0 {
		return true
	}
	normalized := strings.ToLower(strings.Trim(host, "[]"))
	_, ok := s.cfg.allowedHosts[normalized]
	return ok
}

func (s *appServer) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if s.cfg.knownHosts == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	callback, err := knownhosts.New(s.cfg.knownHosts)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return callback, nil
}

func authMethods(req clientMessage) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	password := req.Password
	if password != "" {
		methods = append(methods,
			ssh.Password(password),
			ssh.KeyboardInteractive(func(_, _ string, _ []string, _ []bool) ([]string, error) {
				return []string{password}, nil
			}),
		)
	}
	privateKey := strings.TrimSpace(req.PrivateKey)
	if privateKey != "" {
		signer, err := parsePrivateKey(privateKey, req.Passphrase)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if len(methods) == 0 {
		return nil, errors.New("password or private key is required")
	}
	return methods, nil
}

func parsePrivateKey(key, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase([]byte(key), []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse encrypted private key: %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}

func (s *wsSession) collectInteractiveCredentials(req *clientMessage) error {
	if strings.TrimSpace(req.Username) == "" {
		s.send(serverMessage{Type: "output", Data: "\r\nlogin as: "})
		username, err := s.readPromptLine(req, true)
		if err != nil {
			return err
		}
		req.Username = strings.TrimSpace(username)
		if req.Username == "" {
			return errors.New("username is required")
		}
	}
	if strings.TrimSpace(req.PrivateKey) == "" && req.Password == "" {
		s.send(serverMessage{Type: "output", Data: "password: "})
		password, err := s.readPromptLine(req, false)
		if err != nil {
			return err
		}
		req.Password = password
	}
	return nil
}

func (s *wsSession) readPromptLine(req *clientMessage, echo bool) (string, error) {
	var line []rune
	for {
		var msg clientMessage
		if err := s.conn.ReadJSON(&msg); err != nil {
			return "", err
		}
		switch msg.Type {
		case "input":
			for _, r := range msg.Data {
				switch r {
				case '\r', '\n':
					s.send(serverMessage{Type: "output", Data: "\r\n"})
					return string(line), nil
				case '\u0003':
					s.send(serverMessage{Type: "output", Data: "^C\r\n"})
					return "", errors.New("login cancelled")
				case '\b', '\u007f':
					if len(line) > 0 {
						line = line[:len(line)-1]
						if echo {
							s.send(serverMessage{Type: "output", Data: "\b \b"})
						}
					}
				default:
					if r >= 0x20 && r != 0x7f {
						line = append(line, r)
						if echo {
							s.send(serverMessage{Type: "output", Data: string(r)})
						}
					}
				}
			}
		case "resize":
			req.Cols, req.Rows = saneSize(msg.Cols, msg.Rows)
		case "close":
			return "", errors.New("connection closed")
		}
	}
}

func saneSize(cols, rows int) (int, int) {
	if cols < 20 || cols > 500 {
		cols = 100
	}
	if rows < 5 || rows > 200 {
		rows = 30
	}
	return cols, rows
}

type wsSession struct {
	conn    *websocket.Conn
	client  *ssh.Client
	session *ssh.Session
	stdin   io.Writer
	stdout  io.Reader
	stderr  io.Reader

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

func (s *wsSession) send(msg serverMessage) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.isClosed() {
		return
	}
	if err := s.conn.WriteJSON(msg); err != nil {
		log.Printf("websocket write: %v", err)
	}
}

func (s *wsSession) copyOutput(reader io.Reader) {
	buffer := make([]byte, 8192)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			s.send(serverMessage{Type: "output", Data: string(buffer[:n])})
		}
		if err != nil {
			return
		}
	}
}

func (s *wsSession) wait() {
	err := s.session.Wait()
	if err != nil && !errors.Is(err, io.EOF) {
		s.send(serverMessage{Type: "status", State: "disconnected", Message: err.Error()})
	} else {
		s.send(serverMessage{Type: "status", State: "disconnected", Message: "SSH session closed"})
	}
	s.close()
}

func (s *wsSession) close() {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return
	}
	s.closed = true
	s.closeMu.Unlock()

	if s.session != nil {
		_ = s.session.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *wsSession) isClosed() bool {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closed
}
