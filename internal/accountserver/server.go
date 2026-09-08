package accountserver

import (
	"context"
	"crypto/rand"
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
	"time"

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
	tailnet tailnetProvisioner
	// watchers wakes a device when something it owns changes, so a sign-out or
	// an invitation does not wait out the heartbeat.
	watchers *watchers
}

// NewServer builds the account authority.
func NewServer(config *Config, store *Store, web fs.FS) *Server {
	server := &Server{config: config, store: store, web: web, watchers: newWatchers()}
	if config != nil && config.Tailnet.LoginServer != "" {
		server.tailnet = &headscaleProvisioner{config: config.Tailnet}
	}
	return server
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
	mux.Handle("GET /api/controllers", s.requireAdmin(http.HandlerFunc(s.controllers)))
	mux.Handle("GET /api/networks/mine", s.requireAccount(http.HandlerFunc(s.myNetworks)))
	mux.Handle("GET /api/events", s.requireAccount(http.HandlerFunc(s.serveEvents)))
	mux.Handle("GET /api/networks/{id}/jobs", s.requireAccount(http.HandlerFunc(s.networkJobs)))
	mux.Handle("POST /api/networks/{id}/jobs", s.requireAccount(http.HandlerFunc(s.reportNetworkJobs)))
	mux.Handle("PUT /api/account/profile", s.requireAccount(http.HandlerFunc(s.updateProfile)))
	mux.Handle("PUT /api/account/password", s.requireAccount(http.HandlerFunc(s.changePassword)))
	mux.Handle("GET /api/github/token", s.requireAccount(http.HandlerFunc(s.getGitHubToken)))
	mux.Handle("PUT /api/github/token", s.requireAccount(http.HandlerFunc(s.putGitHubToken)))
	mux.Handle("GET /api/projects/mine", s.requireAccount(http.HandlerFunc(s.myProjects)))
	mux.Handle("POST /api/projects/sync", s.requireAccount(http.HandlerFunc(s.syncProject)))
	mux.Handle("DELETE /api/projects/{id}", s.requireAccount(http.HandlerFunc(s.forgetProject)))
	mux.Handle("GET /api/devices", s.requireAccount(http.HandlerFunc(s.myDevices)))
	mux.Handle("PUT /api/devices/{id}/network", s.requireAccount(http.HandlerFunc(s.setDeviceNetwork)))
	mux.Handle("POST /api/devices/{id}/sign-out", s.requireAccount(http.HandlerFunc(s.signOutDevice)))
	mux.Handle("DELETE /api/devices/{id}", s.requireAccount(http.HandlerFunc(s.removeDevice)))
	mux.Handle("GET /api/accounts/search", s.requireAccount(http.HandlerFunc(s.searchAccounts)))
	mux.Handle("GET /api/invitations", s.requireAccount(http.HandlerFunc(s.myInvitations)))
	mux.Handle("POST /api/invitations/{id}/accept", s.requireAccount(http.HandlerFunc(s.respondToInvitation)))
	mux.Handle("POST /api/invitations/{id}/decline", s.requireAccount(http.HandlerFunc(s.respondToInvitation)))
	mux.Handle("DELETE /api/invitations/{id}", s.requireAccount(http.HandlerFunc(s.revokeInvitation)))
	mux.Handle("GET /api/networks/{id}/invitations", s.requireAccount(http.HandlerFunc(s.networkInvitations)))
	mux.Handle("POST /api/networks/{id}/invitations", s.requireAccount(http.HandlerFunc(s.createInvitation)))
	mux.Handle("POST /api/networks/sync", s.requireAccount(http.HandlerFunc(s.syncNetwork)))
	mux.Handle("DELETE /api/networks/{id}", s.requireAccount(http.HandlerFunc(s.deleteNetwork)))
	mux.Handle("POST /api/networks/{id}/members", s.requireAccount(http.HandlerFunc(s.grantNetworkMember)))
	mux.Handle("PUT /api/networks/{id}/members/{account}", s.requireAccount(http.HandlerFunc(s.updateNetworkMember)))
	mux.Handle("DELETE /api/networks/{id}/members/{account}", s.requireAccount(http.HandlerFunc(s.removeNetworkMember)))
	mux.Handle("POST /api/networks/{id}/rotate-key", s.requireAdmin(http.HandlerFunc(s.rotateNetworkKey)))
	mux.Handle("POST /api/nodes/check-in", s.requireAccount(http.HandlerFunc(s.nodeCheckIn)))
	mux.Handle("POST /api/tailnet/enrollment", s.requireAccount(http.HandlerFunc(s.tailnetEnrollment)))
	mux.Handle("GET /api/controller/context/{id}", s.requireAccount(http.HandlerFunc(s.controllerContext)))
	mux.Handle("PUT /api/controllers/{id}", s.requireAccount(http.HandlerFunc(s.configureController)))
	mux.HandleFunc("POST /api/controllers/{id}/check-in", s.controllerCheckIn)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", s.staticHandler())
	return securityHeaders(mux)
}

func (s *Server) tailnetEnrollment(w http.ResponseWriter, r *http.Request) {
	if s.tailnet == nil || strings.TrimSpace(s.config.Tailnet.LoginServer) == "" {
		fail(w, http.StatusServiceUnavailable, "Automatic private-network enrollment is not configured.")
		return
	}
	account, _ := s.currentAccount(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	enrollment, err := s.tailnet.Enrollment(ctx, account)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, enrollment)
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
	response := map[string]any{"disabled": *body.Disabled}
	if *body.Disabled && s.tailnet != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		err := s.tailnet.Disable(ctx, r.PathValue("id"))
		cancel()
		response["private_network_revoked"] = err == nil
		if err != nil {
			response["warning"] = "The account is disabled, but its private-network devices could not be expired: " + err.Error()
		}
	}
	writeJSON(w, http.StatusOK, response)
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

func (s *Server) controllers(w http.ResponseWriter, _ *http.Request) {
	controllers, err := s.store.Controllers()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, controllers)
}

// myNetworks answers "what am I a member of", which is the question a device
// that has just been signed into has to ask before it can show anything.
func (s *Server) myNetworks(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	networks, err := s.store.NetworksForAccount(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	deleted, err := s.store.DeletedNetworksForAccount(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": networks, "deleted_network_ids": deleted})
}

func (s *Server) syncNetwork(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var input NetworkRegistration
	if err := decode(r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	previousMembers, _ := s.store.NetworkMembers(input.ID)
	network, err := s.store.RegisterNetwork(account.ID, input)
	if err != nil {
		if errors.Is(err, ErrNetworkDeleted) {
			fail(w, http.StatusGone, "This network was deleted.")
		} else if errors.Is(err, ErrNetworkKey) || errors.Is(err, ErrNetworkMember) {
			fail(w, http.StatusForbidden, "This account is not registered for that network or its management key is incorrect.")
		} else {
			fail(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	controller := map[string]string{}
	if item, relayToken, lookupErr := s.store.ControllerForNetwork(input.ID); lookupErr == nil {
		controller = map[string]string{"id": item.ID, "name": item.Name,
			"address": item.PublicURL, "collab_token": relayToken}
	}
	members, err := s.store.NetworkMembers(input.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network": network, "controller": controller, "members": members,
	})
	// Only a newly created network wakes adoption. Notifying every heartbeat
	// would make each awakened device wake all the others again indefinitely.
	if len(previousMembers) == 0 {
		for _, member := range members {
			s.watchers.notify(member.AccountID, TopicNetworks)
		}
	}
}

func (s *Server) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		ManagementKey string `json:"management_key"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	affected, err := s.store.DeleteNetwork(account.ID, r.PathValue("id"), body.ManagementKey)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			fail(w, http.StatusNotFound, "No such network.")
		case errors.Is(err, ErrNetworkKey):
			fail(w, http.StatusForbidden, "The network management key is incorrect.")
		default:
			fail(w, http.StatusForbidden, err.Error())
		}
		return
	}
	for _, accountID := range affected {
		s.watchers.notify(accountID, TopicNetworks)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "affected_accounts": len(affected)})
}

func (s *Server) updateNetworkMember(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		ManagementKey string `json:"management_key"`
		Role          string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetNetworkMemberRole(account.ID, r.PathValue("id"), body.ManagementKey,
		r.PathValue("account"), body.Role); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) removeNetworkMember(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		ManagementKey string `json:"management_key"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.RemoveNetworkMember(account.ID, r.PathValue("id"), body.ManagementKey,
		r.PathValue("account")); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) controllerContext(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	context, err := s.store.ControllerContextFor(account, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !context.CanAccess {
		fail(w, http.StatusForbidden, "This account does not have access to the controller or any network it supplies.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": account, "context": context})
}

func (s *Server) configureController(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var input ControllerConfigure
	if err := decode(r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validControllerURL(input.PublicURL); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	configured, err := s.store.ConfigureController(account, r.PathValue("id"), input)
	if err != nil {
		switch {
		case errors.Is(err, ErrControllerOwner):
			fail(w, http.StatusForbidden, "This controller belongs to another account.")
		case errors.Is(err, ErrControllerAccess):
			fail(w, http.StatusForbidden, "Only a network owner or administrator can supply that network.")
		default:
			fail(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, configured)
}

func validControllerURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("controller public URL must be a complete URL without credentials, query or fragment")
	}
	host := parsed.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return errors.New("controller public URL must use HTTPS (HTTP is allowed only on loopback)")
	}
	return nil
}

func (s *Server) controllerCheckIn(w http.ResponseWriter, r *http.Request) {
	configured, err := s.store.ControllerCheckIn(r.PathValue("id"), bearer(r))
	if err != nil {
		fail(w, http.StatusUnauthorized, "The controller credential is invalid.")
		return
	}
	writeJSON(w, http.StatusOK, configured)
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
	before, discoveryErr := s.store.deviceDiscovery(input.ID)
	if login := strings.TrimSpace(input.GitHubLogin); login != "" && login != account.GitHubLogin {
		_ = s.store.SetGitHubLogin(account.ID, login)
	}
	instructions, err := s.store.CheckIn(account.ID, input)
	if err != nil {
		if errors.Is(err, ErrNodeOwner) {
			fail(w, http.StatusConflict, "That node is registered to another account.")
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if after, err := s.store.deviceDiscovery(input.ID); err == nil && discoveryErr == nil {
		s.notifyDeviceDiscovery(account.ID, before, after)
	}
	// Instructions still travel in the check-in response; the account socket
	// only wakes devices to fetch the changed directory or pending instruction.
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "recorded", "instructions": instructions,
	})
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
