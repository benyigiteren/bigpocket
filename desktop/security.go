package main

// ----------------- Security: Origin Validation, Auth, Path Safety & Stream ACL -----------------
//
// This file centralises every security primitive used by the desktop server:
//   * Origin validation (drive-by RCE protection for the local HTTP/WebSocket server)
//   * Constant-time password verification with per-IP brute-force throttling
//   * Path traversal / Zip Slip safe file resolution
//   * Authorized-IP registry used to gate the raw TCP media ports (8083/8084/8086)
//   * Per-client WebSocket write queues so one slow client cannot stall broadcast()

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ----------------- Host / Origin Validation -----------------

func requestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.Trim(host, "[]")
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

var (
	localIPMu     sync.RWMutex
	localIPCached []string
	localIPAt     time.Time
)

// localInterfaceIPs returns (and briefly caches) the machine's own IP addresses.
func localInterfaceIPs() []string {
	localIPMu.RLock()
	if localIPAt.After(time.Time{}) && time.Since(localIPAt) < 30*time.Second && localIPCached != nil {
		ips := localIPCached
		localIPMu.RUnlock()
		return ips
	}
	localIPMu.RUnlock()

	ips := []string{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}

	localIPMu.Lock()
	localIPCached = ips
	localIPAt = time.Now()
	localIPMu.Unlock()
	return ips
}

func isOwnInterfaceIP(host string) bool {
	for _, ip := range localInterfaceIPs() {
		if ip == host {
			return true
		}
	}
	return false
}

// isAllowedOrigin rejects cross-site browser requests against the local server.
// Native clients (Android app, MCP clients, curl, adb) send no Origin header at all,
// while the WebView2 UI and LAN browser UI send a same-origin http(s)://<host>:8085 value.
func isAllowedOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if origin == "null" {
		// Sandboxed iframes / file:// pages loaded from the local UI.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if p := u.Port(); p != "" && p != "8085" {
		return false
	}
	return isLoopbackHost(host) || isOwnInterfaceIP(host)
}

// originGuard wraps the HTTP mux and refuses requests coming from a foreign origin.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedOrigin(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"error":"Forbidden origin"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setCORS mirrors the request origin instead of blindly allowing every website.
func setCORS(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && isAllowedOrigin(r) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
}

// ----------------- Password Verification & Brute-force Protection -----------------

const (
	authMaxFailures   = 5
	authFailWindow    = 5 * time.Minute
	authBlockDuration = 5 * time.Minute
)

type authFailEntry struct {
	count        int
	firstFailure time.Time
	blockedUntil time.Time
}

var (
	authFailMu sync.Mutex
	authFails  = make(map[string]*authFailEntry)
)

// constantTimeEqual compares two secrets without leaking length or content through timing.
func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func isBruteForceBlocked(ip string) bool {
	authFailMu.Lock()
	defer authFailMu.Unlock()
	e, ok := authFails[ip]
	if !ok {
		return false
	}
	if time.Now().Before(e.blockedUntil) {
		return true
	}
	if time.Since(e.firstFailure) > authFailWindow {
		delete(authFails, ip)
	}
	return false
}

func recordAuthFailure(ip string) {
	authFailMu.Lock()
	defer authFailMu.Unlock()
	e, ok := authFails[ip]
	if !ok || time.Since(e.firstFailure) > authFailWindow {
		e = &authFailEntry{firstFailure: time.Now()}
		authFails[ip] = e
	}
	e.count++
	if e.count >= authMaxFailures {
		e.blockedUntil = time.Now().Add(authBlockDuration)
	}
}

func recordAuthSuccess(ip string) {
	authFailMu.Lock()
	delete(authFails, ip)
	authFailMu.Unlock()
}

// verifyRequestPassword returns true when the request carries the configured password.
// The caller must already have handled the "no password configured" case.
func verifyRequestPassword(r *http.Request, expected string) bool {
	pw := r.Header.Get("X-Password")
	if pw == "" {
		pw = r.URL.Query().Get("password")
	}
	return constantTimeEqual(pw, expected)
}

// ----------------- Authorized Stream IPs (TCP 8083 / 8084 / 8086) -----------------

const authorizedIPTTL = 12 * time.Hour

var (
	authorizedIPMu sync.RWMutex
	authorizedIPs  = make(map[string]time.Time)
)

// registerAuthorizedIP marks an IP as trusted for the raw media TCP ports.
func registerAuthorizedIP(ip string) {
	ip = strings.Trim(strings.TrimSpace(ip), "[]")
	if ip == "" {
		return
	}
	authorizedIPMu.Lock()
	authorizedIPs[ip] = time.Now()
	authorizedIPMu.Unlock()
}

func addrHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	return strings.Trim(host, "[]")
}

// isAuthorizedStreamIP decides whether a raw media connection (camera/audio/screen)
// may be accepted. Loopback is always allowed; when no password is configured the
// whole LAN is trusted (there is nothing to authenticate against); otherwise only
// IPs that already authenticated over HTTP/WebSocket are accepted.
func isAuthorizedStreamIP(addr net.Addr) bool {
	host := addrHost(addr)
	if isLoopbackHost(host) {
		return true
	}
	if loadConfig().Password == "" {
		return true
	}
	authorizedIPMu.RLock()
	at, ok := authorizedIPs[host]
	authorizedIPMu.RUnlock()
	return ok && time.Since(at) < authorizedIPTTL
}

// ----------------- HTTP authorization -----------------

func isAuthorized(r *http.Request) bool {
	if !isAllowedOrigin(r) {
		return false
	}
	host := requestHost(r)
	if isLoopbackHost(host) {
		registerAuthorizedIP(host)
		return true
	}
	if isBruteForceBlocked(host) {
		return false
	}
	cfg := loadConfig()
	if cfg.Password == "" {
		registerAuthorizedIP(host)
		return true
	}
	if verifyRequestPassword(r, cfg.Password) {
		recordAuthSuccess(host)
		registerAuthorizedIP(host)
		return true
	}
	recordAuthFailure(host)
	return false
}

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"error":"Unauthorized"}`))
		return false
	}
	return true
}

// ----------------- Safe Path Resolution (Path Traversal & Zip Slip) -----------------

// safeJoinPath resolves name inside baseDir and refuses anything that would escape it.
func safeJoinPath(baseDir, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("empty file name")
	}
	if strings.ContainsRune(name, '\x00') {
		return "", errors.New("invalid file name")
	}
	clean := filepath.Base(filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")))
	if clean == "" || clean == "." || clean == ".." || clean == string(filepath.Separator) {
		return "", errors.New("invalid file name")
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(baseAbs, clean)
	rel, err := filepath.Rel(baseAbs, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes base directory")
	}
	return full, nil
}

// safeSharedFilePath resolves a requested shared-file name inside the shared folder.
func safeSharedFilePath(filename string) (string, error) {
	filename = strings.TrimPrefix(filename, "/")
	return safeJoinPath(sharedDir, filename)
}

// safeZipEntryPath resolves a (possibly nested) archive entry path inside baseDir
// and refuses absolute paths or any "../" escape (Zip Slip protection).
func safeZipEntryPath(baseDir, entryName string) (string, error) {
	name := strings.ReplaceAll(entryName, `\`, "/")
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", errors.New("invalid archive entry: " + entryName)
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(baseAbs, clean)
	rel, err := filepath.Rel(baseAbs, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("archive entry escapes destination: " + entryName)
	}
	return full, nil
}

// ----------------- WebSocket Client Registry -----------------
//
// Every connected client gets its own write lock, so a slow/stalled socket can only
// block itself; broadcast() never waits behind a single unresponsive client.

type wsClientConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

var (
	websocketClients   = make(map[*websocket.Conn]*wsClientConn)
	websocketClientsMu sync.Mutex
)

func addWSClient(conn *websocket.Conn) *wsClientConn {
	c := &wsClientConn{conn: conn}
	websocketClientsMu.Lock()
	websocketClients[conn] = c
	websocketClientsMu.Unlock()
	return c
}

func removeWSClient(conn *websocket.Conn) {
	websocketClientsMu.Lock()
	delete(websocketClients, conn)
	websocketClientsMu.Unlock()
}

func wsClientCount() int {
	websocketClientsMu.Lock()
	n := len(websocketClients)
	websocketClientsMu.Unlock()
	return n
}

func wsClientSnapshot() []*websocket.Conn {
	websocketClientsMu.Lock()
	clients := make([]*websocket.Conn, 0, len(websocketClients))
	for conn := range websocketClients {
		clients = append(clients, conn)
	}
	websocketClientsMu.Unlock()
	return clients
}

// safeWriteWS writes with a per-connection lock and deadline. Clients that are not
// registered yet (pre-upgrade handshake messages) fall back to a direct write.
func safeWriteWS(conn *websocket.Conn, msgType int, data []byte) error {
	websocketClientsMu.Lock()
	c := websocketClients[conn]
	websocketClientsMu.Unlock()

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if c == nil {
		return conn.WriteMessage(msgType, data)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return conn.WriteMessage(msgType, data)
}
