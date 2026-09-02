package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/auth"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// sessionCookie names the browser session. It is HttpOnly and SameSite=Strict,
// so no script can read it and no other site can send it.
const sessionCookie = "plainshow_session"

// sessionLifetime is how long a signed-in browser stays signed in.
const sessionLifetime = 30 * 24 * time.Hour

type authContextKey struct{}

// authenticate gates the API and the event stream on a signed-in session.
//
// Authentication is opt-in: a node with no owner account configured serves
// everything, which is what makes the first run possible at all. It binds to
// loopback by default, so an unconfigured node is reachable only from the
// machine it runs on. Once an owner exists, every API call needs a session.
//
// Static assets stay public deliberately. The interface has to load in order
// to show a sign-in form, and it contains nothing worth protecting.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/ws" &&
			!strings.HasPrefix(r.URL.Path, "/jupyter/") {
			next.ServeHTTP(w, r)
			return
		}

		enabled, err := s.store.AuthEnabled()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.usesCentralAccounts() {
			enabled = true
		}
		if !enabled {
			next.ServeHTTP(w, r)
			return
		}

		// The command line presents the install root's local token instead of
		// a session. Reading that file already implies read access to the
		// database beside it, so this widens nothing — it just keeps the CLI
		// working once the node has an owner, without loosening the rules the
		// browser is held to.
		if s.localToken != "" {
			if presented, ok := bearerToken(r); ok &&
				subtle.ConstantTimeCompare([]byte(presented), []byte(s.localToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			fail(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		account, err := s.store.SessionAccount(cookie.Value)
		if err != nil {
			clearSessionCookie(w, r)
			fail(w, http.StatusUnauthorized, "Your session expired. Sign in again.")
			return
		}

		// A cookie alone would let any other site drive this node through the
		// signed-in browser, so anything that changes state must also prove it
		// came from here.
		if !safeMethod(r.Method) && !sameOrigin(r) {
			fail(w, http.StatusForbidden, "Cross-origin request refused.")
			return
		}

		ctx := context.WithValue(r.Context(), authContextKey{}, account)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken reads an Authorization: Bearer header.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// sameOrigin reports whether a state-changing request came from this node's own
// interface.
//
// A missing Origin is accepted: browsers omit it on same-origin navigations,
// and the CLI and other non-browser callers never send one. The header cannot
// be forged by a page, which is what makes the check worth anything.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// accountFrom returns the signed-in account, when there is one.
func accountFrom(r *http.Request) (store.Account, bool) {
	account, ok := r.Context().Value(authContextKey{}).(store.Account)
	return account, ok
}

// authStatus tells the interface whether to show a sign-in form, a first-run
// setup form, or the workspace.
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.store.AuthEnabled()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	central := s.usesCentralAccounts()
	if central {
		enabled = s.cfg.Account.ID != ""
	}
	response := map[string]any{"enabled": enabled, "authenticated": false,
		"central": central, "account_server": s.cfg.Account.Server}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if account, err := s.store.SessionAccount(cookie.Value); err == nil {
			response["authenticated"] = true
			response["account"] = account
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// authSetup creates the owner account on a node that has none, and signs the
// caller in as that owner.
func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if s.usesCentralAccounts() {
		s.centralRegister(w, r)
		return
	}
	enabled, err := s.store.AuthEnabled()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if enabled {
		fail(w, http.StatusConflict, "An owner account is already configured.")
		return
	}

	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	body.Username = strings.TrimSpace(body.Username)
	if !validUsername(body.Username) {
		fail(w, http.StatusBadRequest,
			"Pick a username of 2–48 characters, without spaces, @, : or slashes.")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetAccountPassword(s.cfg.Node.ID, body.Username, body.DisplayName, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The account row is created when the node is initialised. Reaching
			// here means the node's identity and its database disagree, which a
			// new password cannot fix.
			fail(w, http.StatusInternalServerError,
				"This node has no owner record to claim. Re-run `pscluster init`.")
			return
		}
		fail(w, http.StatusConflict, "That username is already in use.")
		return
	}

	account, err := s.store.Account(s.cfg.Node.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueSession(w, r, account); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"account": account, "authenticated": true,
	})
}

// validUsername keeps a username usable as an identifier everywhere it appears.
func validUsername(name string) bool {
	if len(name) < 2 || len(name) > 48 {
		return false
	}
	return !strings.ContainsAny(name, " \t\n/\\@:")
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.usesCentralAccounts() {
		s.centralLogin(w, r)
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
	if err != nil || !auth.VerifyPassword(account.PasswordHash, body.Password) {
		// One message and one delay for both "no such user" and "wrong
		// password", so the response cannot be used to enumerate accounts.
		time.Sleep(150 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "Username or password is incorrect.")
		return
	}
	if err := s.issueSession(w, r, account); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account": account, "authenticated": true,
	})
}

func (s *Server) usesCentralAccounts() bool {
	return strings.TrimSpace(s.cfg.Account.Server) != ""
}

func (s *Server) centralRegister(w http.ResponseWriter, r *http.Request) {
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
	client, err := accountclient.New(s.cfg.Account.Server)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	response, err := client.Register(ctx, strings.TrimSpace(body.Username),
		strings.TrimSpace(body.DisplayName), body.Password, strings.TrimSpace(body.BootstrapToken))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.finishCentralAuth(w, r, response)
}

func (s *Server) centralLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := accountclient.New(s.cfg.Account.Server)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	response, err := client.Login(ctx, strings.TrimSpace(body.Username), body.Password)
	if err != nil {
		fail(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.finishCentralAuth(w, r, response)
}

func (s *Server) finishCentralAuth(w http.ResponseWriter, r *http.Request, response accountclient.AuthResponse) {
	if response.Account.ID == "" || response.Token == "" {
		fail(w, http.StatusBadGateway, "The Account Server returned an incomplete identity.")
		return
	}
	if s.cfg.Account.ID != "" && s.cfg.Account.ID != response.Account.ID {
		fail(w, http.StatusConflict, "This device is already linked to another Plainshow account.")
		return
	}
	local := centralAccount(response.Account)
	if err := s.store.AdoptGlobalAccount(s.cfg.AccountID(), local); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg.Account.ID = local.ID
	s.cfg.Account.Username = local.Username
	s.cfg.Account.DisplayName = local.DisplayName
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := config.SaveAccountToken(s.layout, response.Token); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueSession(w, r, local); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	go s.checkInAccountServer(context.Background())
	writeJSON(w, http.StatusOK, map[string]any{"account": local, "authenticated": true})
}

func centralAccount(local accountserver.Account) store.Account {
	return store.Account{ID: local.ID, Username: local.Username, DisplayName: local.DisplayName}
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(cookie.Value)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

// issueSession mints a session and sets its cookie.
//
// The token is random, and the store keeps only its hash: a copy of the
// database is not a set of usable sessions.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, account store.Account) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(sessionLifetime)
	if err := s.store.CreateSession(token, account.ID, expires); err != nil {
		return err
	}
	http.SetCookie(w, sessionCookieValue(r, token, expires, int(sessionLifetime.Seconds())))
	return nil
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, sessionCookieValue(r, "", time.Time{}, -1))
}

// sessionCookieValue builds the cookie both paths must agree on. Secure is set
// only under TLS, because a Secure cookie on plain http://localhost is simply
// discarded by the browser.
func sessionCookieValue(r *http.Request, value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	}
}
