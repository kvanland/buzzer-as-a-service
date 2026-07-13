package buzzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxJSONBodyBytes           int64 = 16 << 10
	rateWindow                       = time.Minute
	maxCreateRequestsPerWindow       = 20
	maxActionRequestsPerWindow       = 300
	maxEventStreamsPerIP             = 80
	rateClientTTL                    = 10 * time.Minute
)

type Server struct {
	store   *Store
	assets  http.Handler
	limiter *rateLimiter
}

func NewServer(store *Store, embedded fs.FS) http.Handler {
	return &Server{
		store:   store,
		assets:  http.FileServer(http.FS(embedded)),
		limiter: newRateLimiter(),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/buzzer-as-a-service")
	if path == "" {
		path = "/"
	}
	if path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if strings.HasPrefix(path, "/api/") {
		s.handleAPI(w, r, path)
		return
	}
	if path == "/" {
		r.URL.Path = "/"
	}
	s.assets.ServeHTTP(w, r)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, path string) {
	client := clientIP(r)
	if path == "/api/groups" && r.Method == http.MethodPost {
		if !s.limiter.allowCreate(client) {
			writeRateLimit(w)
			return
		}
		var req struct {
			HostName string `json:"hostName"`
			Color    string `json:"color"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := s.store.CreateGroup(req.HostName, req.Color)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
		return
	}

	if r.Method == http.MethodPost && !s.limiter.allowAction(client) {
		writeRateLimit(w)
		return
	}

	parts := strings.Split(strings.TrimPrefix(path, "/api/groups/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	code := parts[0]
	action := parts[1]

	switch {
	case action == "events" && r.Method == http.MethodGet:
		s.handleEvents(w, r, code)
	case action == "join" && r.Method == http.MethodPost:
		s.handleJoin(w, r, code)
	case action == "player-session" && r.Method == http.MethodPost:
		s.handlePlayerSession(w, r, code)
	case action == "host-session" && r.Method == http.MethodPost:
		s.handleHostSession(w, r, code)
	case action == "profile" && r.Method == http.MethodPost:
		s.handleProfile(w, r, code)
	case action == "buzz" && r.Method == http.MethodPost:
		s.handleBuzz(w, r, code)
	case action == "reset" && r.Method == http.MethodPost:
		s.handleReset(w, r, code)
	case action == "reset-round-count" && r.Method == http.MethodPost:
		s.handleResetRoundCount(w, r, code)
	case action == "lock-all" && r.Method == http.MethodPost:
		s.handleLockAll(w, r, code)
	case action == "players" && len(parts) == 4 && parts[2] != "" && parts[3] == "lock" && r.Method == http.MethodPost:
		s.handlePlayerLock(w, r, code, parts[2])
	case action == "players" && len(parts) == 4 && parts[2] != "" && parts[3] == "remove" && r.Method == http.MethodPost:
		s.handlePlayerRemove(w, r, code, parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		PlayerID    string `json:"playerId"`
		PlayerToken string `json:"playerToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.JoinGroup(code, req.Name, req.Color, req.PlayerID, req.PlayerToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePlayerSession(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		PlayerID    string `json:"playerId"`
		PlayerToken string `json:"playerToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.PlayerReconnect(code, req.PlayerID, req.PlayerToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleHostSession(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		HostToken string `json:"hostToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.HostReconnect(code, req.HostToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		PlayerID    string `json:"playerId"`
		PlayerToken string `json:"playerToken"`
		Name        string `json:"name"`
		Color       string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.UpdatePlayer(code, req.PlayerID, req.PlayerToken, req.Name, req.Color)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBuzz(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		PlayerID    string `json:"playerId"`
		PlayerToken string `json:"playerToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.store.Buzz(code, req.PlayerID, req.PlayerToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		HostToken string `json:"hostToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snapshot, err := s.store.Reset(code, req.HostToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleResetRoundCount(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		HostToken string `json:"hostToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snapshot, err := s.store.ResetRoundCount(code, req.HostToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleLockAll(w http.ResponseWriter, r *http.Request, code string) {
	var req struct {
		HostToken string `json:"hostToken"`
		Locked    bool   `json:"locked"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snapshot, err := s.store.SetLockAll(code, req.HostToken, req.Locked)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handlePlayerLock(w http.ResponseWriter, r *http.Request, code, playerID string) {
	var req struct {
		HostToken string `json:"hostToken"`
		Locked    bool   `json:"locked"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snapshot, err := s.store.SetPlayerLock(code, req.HostToken, playerID, req.Locked)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handlePlayerRemove(w http.ResponseWriter, r *http.Request, code, playerID string) {
	var req struct {
		HostToken string `json:"hostToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snapshot, err := s.store.RemovePlayer(code, req.HostToken, playerID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, code string) {
	release, ok := s.limiter.acquireEvent(clientIP(r))
	if !ok {
		writeRateLimit(w)
		return
	}
	defer release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel, err := s.store.Subscribe(code)
	if err != nil {
		writeSSEError(w, flusher, err)
		return
	}
	defer cancel()

	for {
		select {
		case <-r.Context().Done():
			return
		case snapshot, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(snapshot)
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); ip != "" {
		return ip
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if i := strings.IndexByte(forwarded, ','); i >= 0 {
			forwarded = forwarded[:i]
		}
		if ip := strings.TrimSpace(forwarded); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty json"})
			return false
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

func writeRateLimit(w http.ResponseWriter) {
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": ErrLimit.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrExpired):
		status = http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrLocked):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrLimit):
		status = http.StatusTooManyRequests
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, err error) {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
	flusher.Flush()
}

type rateLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	clients map[string]*clientRate
}

type clientRate struct {
	createWindow time.Time
	createCount  int
	actionWindow time.Time
	actionCount  int
	eventStreams int
	lastSeen     time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		now:     time.Now,
		clients: make(map[string]*clientRate),
	}
}

func (l *rateLimiter) allowCreate(ip string) bool {
	return l.allow(ip, maxCreateRequestsPerWindow, true)
}

func (l *rateLimiter) allowAction(ip string) bool {
	return l.allow(ip, maxActionRequestsPerWindow, false)
}

func (l *rateLimiter) allow(ip string, limit int, create bool) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	client := l.clientLocked(ip, now)
	window := &client.actionWindow
	count := &client.actionCount
	if create {
		window = &client.createWindow
		count = &client.createCount
	}
	if window.IsZero() || now.Sub(*window) >= rateWindow {
		*window = now
		*count = 0
	}
	if *count >= limit {
		return false
	}
	*count++
	client.lastSeen = now
	return true
}

func (l *rateLimiter) acquireEvent(ip string) (func(), bool) {
	now := l.now()
	l.mu.Lock()
	l.cleanupLocked(now)
	client := l.clientLocked(ip, now)
	if client.eventStreams >= maxEventStreamsPerIP {
		l.mu.Unlock()
		return nil, false
	}
	client.eventStreams++
	client.lastSeen = now
	l.mu.Unlock()

	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		if client := l.clients[ip]; client != nil && client.eventStreams > 0 {
			client.eventStreams--
			client.lastSeen = l.now()
		}
	}, true
}

func (l *rateLimiter) clientLocked(ip string, now time.Time) *clientRate {
	client := l.clients[ip]
	if client == nil {
		client = &clientRate{lastSeen: now}
		l.clients[ip] = client
	}
	return client
}

func (l *rateLimiter) cleanupLocked(now time.Time) {
	for ip, client := range l.clients {
		if client.eventStreams == 0 && now.Sub(client.lastSeen) > rateClientTTL {
			delete(l.clients, ip)
		}
	}
}
