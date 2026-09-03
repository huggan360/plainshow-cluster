package accountserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/huggan360/plainshow-cluster/internal/auth"
	"github.com/huggan360/plainshow-cluster/internal/brand"
	"github.com/huggan360/plainshow-cluster/internal/config"
)

const (
	adminSessionCookie = "plainshow_admin_session"
	sessionLifetime    = 30 * 24 * time.Hour
)

// Server serves global identity APIs and the deliberately small admin page.
type Server struct {
	config  *Config
	store   *Store
	web     fs.FS
	mu      sync.RWMutex
	writeMu sync.Mutex
	sockets map[string]map[*websocket.Conn]bool
}

// NewServer builds the account authority.
func NewServer(config *Config, store *Store, web fs.FS) *Server {
	return &Server{config: config, store: store, web: web,
		sockets: map[string]map[*websocket.Conn]bool{}}
}

// Handler returns the public registration/login surface and authenticated
// administration endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /brand/plainshow-icon.webp", brand.ServeIcon)
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.Handle("GET /api/stats", s.requireAdmin(http.HandlerFunc(s.stats)))
	mux.Handle("GET /api/accounts", s.requireAdmin(http.HandlerFunc(s.accounts)))
	mux.Handle("PATCH /api/accounts/{id}", s.requireAdmin(http.HandlerFunc(s.updateAccount)))
	mux.Handle("GET /api/nodes", s.requireAdmin(http.HandlerFunc(s.nodes)))
	mux.Handle("GET /api/networks", s.requireAdmin(http.HandlerFunc(s.networks)))
	mux.Handle("POST /api/networks/sync", s.requireAccount(http.HandlerFunc(s.syncNetwork)))
	mux.Handle("POST /api/networks/{id}/members", s.requireAccount(http.HandlerFunc(s.grantNetworkMember)))
	mux.Handle("POST /api/networks/{id}/rotate-key", s.requireAdmin(http.HandlerFunc(s.rotateNetworkKey)))
	mux.Handle("POST /api/nodes/check-in", s.requireAccount(http.HandlerFunc(s.nodeCheckIn)))
	mux.HandleFunc("GET /ws", s.collaborationSocket)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", s.staticHandler())
	return securityHeaders(mux)
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	account, ok := s.currentAccount(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": ok, "account": account,
		"registration_open": s.config.RegistrationOpen,
		"public_url":        s.config.PublicURL,
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, http.StatusForbidden, "Cross-origin registration refused.")
		return
	}
	var body struct {
		Username       string `json:"username"`
		DisplayName    string `json:"display_name"`
		Password       string `json:"password"`
		BootstrapToken string `json:"bootstrap_token"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if !validUsername(body.Username) {
		fail(w, http.StatusBadRequest, "Use 2–48 letters, numbers, dots, dashes or underscores for the username.")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}
	if len(body.DisplayName) > 80 {
		fail(w, http.StatusBadRequest, "Display name may contain at most 80 characters.")
		return
	}
	passwordHash, err := auth.HashPassword(body.Password)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	account := Account{ID: config.NewID(), Username: body.Username,
		DisplayName: body.DisplayName, PasswordHash: passwordHash}
	if err := s.store.CreateAccount(account, TokenHash(body.BootstrapToken), s.config.RegistrationOpen); err != nil {
		switch {
		case errors.Is(err, ErrBootstrapToken):
			fail(w, http.StatusForbidden, "The first account needs the bootstrap token printed by pscluster-admin init.")
		case errors.Is(err, ErrRegistrationClosed):
			fail(w, http.StatusForbidden, "Account registration is closed.")
		default:
			fail(w, http.StatusConflict, "That username is already registered.")
		}
		return
	}
	created, err := s.store.AccountByUsername(account.Username)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	token, err := s.issueSession(w, r, created)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"account": created, "token": token})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, http.StatusForbidden, "Cross-origin login refused.")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	account, err := s.store.AccountByUsername(strings.TrimSpace(body.Username))
	if err != nil || account.Disabled || !auth.VerifyPassword(account.PasswordHash, body.Password) {
		time.Sleep(150 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "Username or password is incorrect.")
		return
	}
	token, err := s.issueSession(w, r, account)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": account, "token": token})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionToken(r); token != "" {
		_ = s.store.DeleteSession(token)
	}
	http.SetCookie(w, sessionCookie(r, "", time.Time{}, -1))
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) stats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.store.Stats()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) accounts(w http.ResponseWriter, _ *http.Request) {
	accounts, err := s.store.Accounts()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	current, _ := s.currentAccount(r)
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if err := decode(r, &body); err != nil || body.Disabled == nil {
		fail(w, http.StatusBadRequest, "Supply disabled as true or false.")
		return
	}
	if current.ID == r.PathValue("id") && *body.Disabled {
		fail(w, http.StatusConflict, "You cannot disable the account you are signed in with.")
		return
	}
	if err := s.store.SetAccountDisabled(r.PathValue("id"), *body.Disabled); err != nil {
		fail(w, http.StatusNotFound, "No such account.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disabled": *body.Disabled})
}

func (s *Server) nodes(w http.ResponseWriter, _ *http.Request) {
	nodes, err := s.store.Nodes()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) networks(w http.ResponseWriter, _ *http.Request) {
	networks, err := s.store.Networks()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, networks)
}

func (s *Server) syncNetwork(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var input NetworkRegistration
	if err := decode(r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	network, err := s.store.RegisterNetwork(account.ID, input)
	if err != nil {
		if errors.Is(err, ErrNetworkKey) || errors.Is(err, ErrNetworkMember) {
			fail(w, http.StatusForbidden, "This account is not registered for that network or its management key is incorrect.")
		} else {
			fail(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	publicURL := s.config.PublicURL
	if publicURL == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		publicURL = scheme + "://" + r.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network": network,
		"controller": map[string]string{
			"id": "plainshow-enterprise", "name": "Plainshow Enterprise",
			"address": strings.TrimRight(publicURL, "/"), "collab_token": network.CollabToken,
		},
	})
}

func (s *Server) rotateNetworkKey(w http.ResponseWriter, r *http.Request) {
	key, err := s.store.RotateNetworkKey(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such network.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"management_key": key})
}

func (s *Server) grantNetworkMember(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		ManagementKey string `json:"management_key"`
		AccountID     string `json:"account_id"`
		Role          string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.GrantNetworkMember(account.ID, r.PathValue("id"),
		body.ManagementKey, body.AccountID, body.Role); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "member registered"})
}

var accountWSUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func (s *Server) collaborationSocket(w http.ResponseWriter, r *http.Request) {
	networkID := r.URL.Query().Get("network")
	protocol := ""
	for _, offered := range websocket.Subprotocols(r) {
		if strings.HasPrefix(offered, "plainshow.") {
			protocol = offered
			break
		}
	}
	token := strings.TrimPrefix(protocol, "plainshow.")
	want, err := s.store.NetworkCollabToken(networkID)
	if err != nil || protocol == "" || len(token) != len(want) ||
		subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		fail(w, http.StatusUnauthorized, "The collaboration key is invalid.")
		return
	}
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", protocol)
	conn, err := accountWSUpgrader.Upgrade(w, r, header)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.sockets[networkID] == nil {
		s.sockets[networkID] = map[*websocket.Conn]bool{}
	}
	s.sockets[networkID][conn] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sockets[networkID], conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	conn.SetReadLimit(2 << 20)
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.mu.RLock()
		peers := make([]*websocket.Conn, 0, len(s.sockets[networkID]))
		for peer := range s.sockets[networkID] {
			if peer != conn {
				peers = append(peers, peer)
			}
		}
		s.mu.RUnlock()
		for _, peer := range peers {
			s.writeMu.Lock()
			_ = peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = peer.WriteMessage(kind, payload)
			s.writeMu.Unlock()
		}
	}
}

func (s *Server) nodeCheckIn(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var input NodeCheckIn
	if err := decode(r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ID == "" || strings.TrimSpace(input.Name) == "" || len(input.Networks) > 100 ||
		input.GPUCount < 0 || input.ProjectCount < 0 || input.RunningJobs < 0 {
		fail(w, http.StatusBadRequest, "The node check-in is incomplete or invalid.")
		return
	}
	if err := s.store.CheckIn(account.ID, input); err != nil {
		if errors.Is(err, ErrNodeOwner) {
			fail(w, http.StatusConflict, "That node is registered to another account.")
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

func (s *Server) requireAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentAccount(r); !ok {
			fail(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		if !safeMethod(r.Method) && !sameOrigin(r) && bearer(r) == "" {
			fail(w, http.StatusForbidden, "Cross-origin request refused.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, _ := s.currentAccount(r)
		if !account.Admin {
			fail(w, http.StatusForbidden, "Administrator access is required.")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) currentAccount(r *http.Request) (Account, bool) {
	token := sessionToken(r)
	if token == "" {
		return Account{}, false
	}
	account, err := s.store.SessionAccount(token)
	return account, err == nil
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, account Account) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	expires := time.Now().Add(sessionLifetime)
	if err := s.store.CreateSession(token, account.ID, expires); err != nil {
		return "", err
	}
	http.SetCookie(w, sessionCookie(r, token, expires, int(sessionLifetime.Seconds())))
	return token, nil
}

func sessionToken(r *http.Request) string {
	if token := bearer(r); token != "" {
		return token
	}
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func bearer(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func sessionCookie(r *http.Request, value string, expires time.Time, maxAge int) *http.Cookie {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	return &http.Cookie{Name: adminSessionCookie, Value: value, Path: "/", Expires: expires,
		MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
}

func validUsername(name string) bool {
	if len(name) < 2 || len(name) > 48 {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func decode(r *http.Request, output any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("could not read the request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) staticHandler() http.Handler {
	files := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if r.URL.Path == "/" {
			raw, err := fs.ReadFile(s.web, "index.html")
			if err != nil {
				http.Error(w, "admin interface is unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(raw)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the loopback service for an external TLS proxy.
func (s *Server) ListenAndServe(ctx context.Context, onReady func(string)) error {
	address := net.JoinHostPort(s.config.Listen.Bind, strconv.Itoa(s.config.Listen.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("admin address %s is not available: %w", address, err)
	}
	if onReady != nil {
		onReady("http://" + address)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
