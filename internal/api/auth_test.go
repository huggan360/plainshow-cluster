package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// newTestServer builds a server over a throwaway node. Only the pieces the
// authentication middleware touches are wired: the rest stay nil, and any test
// that needs them will fail loudly rather than silently pass.
func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()

	layout, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Defaults()
	// `init` creates the owner's account row before any password exists; setup
	// claims it. Bootstrapping the same way keeps these tests on the real path.
	if err := st.UpsertAccount(store.Account{
		ID: cfg.Node.ID, Username: "owner-" + cfg.Node.ID, Created: store.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		cfg: cfg, layout: layout, store: st, hub: events.NewHub(),
		web: os.DirFS(t.TempDir()),
	}

	// Exercise the middleware over a stand-in, so these tests describe the
	// gate itself rather than whatever a real handler happens to return.
	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/auth/status", srv.authStatus)
	inner.HandleFunc("POST /api/auth/setup", srv.authSetup)
	inner.HandleFunc("POST /api/auth/login", srv.authLogin)
	inner.HandleFunc("POST /api/auth/logout", srv.authLogout)
	inner.HandleFunc("/api/protected", func(w http.ResponseWriter, r *http.Request) {
		account, ok := accountFrom(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"signed_in": ok, "username": account.Username,
		})
	})
	return srv, srv.authenticate(inner)
}

func post(t *testing.T, h http.Handler, path, body string, cookie *http.Cookie,
	headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sessionFrom pulls the session cookie out of a response.
func sessionFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no session cookie in response: %s", rec.Body.String())
	return nil
}

// TestUnconfiguredNodeIsOpen documents the bootstrap rule deliberately: with no
// owner account there is nobody to authenticate as, so the API answers. The
// node binds to loopback by default, which is what contains this.
func TestUnconfiguredNodeIsOpen(t *testing.T) {
	_, h := newTestServer(t)
	if rec := get(t, h, "/api/protected", nil); rec.Code != http.StatusOK {
		t.Fatalf("unconfigured node refused a request: %d", rec.Code)
	}
}

func TestSetupThenEnforced(t *testing.T) {
	_, h := newTestServer(t)

	rec := post(t, h, "/api/auth/setup",
		`{"username":"hugo","display_name":"Hugo","password":"a-long-enough-password"}`, nil, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	cookie := sessionFrom(t, rec)

	if rec := get(t, h, "/api/protected", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("after setup, an anonymous request got %d, want 401", rec.Code)
	}
	rec = get(t, h, "/api/protected", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner's own session was refused: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		SignedIn bool   `json:"signed_in"`
		Username string `json:"username"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.SignedIn || body.Username != "hugo" {
		t.Errorf("handler did not see the account: %+v", body)
	}
}

func TestSetupRefusesSecondOwner(t *testing.T) {
	_, h := newTestServer(t)
	post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)

	rec := post(t, h, "/api/auth/setup",
		`{"username":"albin","password":"another-long-password"}`, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("a second owner was accepted: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupRejectsWeakInput(t *testing.T) {
	cases := []struct{ name, body string }{
		{"short password", `{"username":"hugo","password":"short"}`},
		{"empty username", `{"username":"","password":"a-long-enough-password"}`},
		{"username with space", `{"username":"hugo hansson","password":"a-long-enough-password"}`},
		{"username with slash", `{"username":"a/b","password":"a-long-enough-password"}`},
		{"username with at", `{"username":"a@b","password":"a-long-enough-password"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, h := newTestServer(t)
			rec := post(t, h, "/api/auth/setup", tc.body, nil, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("accepted %s: %d %s", tc.name, rec.Code, rec.Body.String())
			}
			// And the node must still be unconfigured, not half-configured.
			if rec := get(t, h, "/api/auth/status", nil); !strings.Contains(rec.Body.String(), `"enabled":false`) {
				t.Errorf("a rejected setup left the node configured: %s", rec.Body.String())
			}
		})
	}
}

func TestLoginWrongPassword(t *testing.T) {
	_, h := newTestServer(t)
	post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)

	rec := post(t, h, "/api/auth/login", `{"username":"hugo","password":"wrong-password-x"}`, nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password accepted: %d", rec.Code)
	}
	// The same answer for an unknown account, so responses cannot enumerate.
	other := post(t, h, "/api/auth/login", `{"username":"nobody","password":"wrong-password-x"}`, nil, nil)
	if other.Code != rec.Code || other.Body.String() != rec.Body.String() {
		t.Errorf("unknown user and wrong password differ:\n  %d %s\n  %d %s",
			rec.Code, rec.Body.String(), other.Code, other.Body.String())
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	_, h := newTestServer(t)
	rec := post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)
	cookie := sessionFrom(t, rec)

	if rec := post(t, h, "/api/auth/logout", `{}`, cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", rec.Code)
	}
	// The token must be dead server-side, not merely cleared in the browser.
	if rec := get(t, h, "/api/protected", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("a signed-out session still worked: %d", rec.Code)
	}
}

func TestForgedSessionRefused(t *testing.T) {
	_, h := newTestServer(t)
	post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)

	forged := &http.Cookie{Name: sessionCookie, Value: "not-a-real-token"}
	if rec := get(t, h, "/api/protected", forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("a made-up session token was accepted: %d", rec.Code)
	}
}

// TestCrossOriginWriteRefused covers the reason the cookie alone is not enough:
// another site must not be able to drive this node through a signed-in browser.
func TestCrossOriginWriteRefused(t *testing.T) {
	_, h := newTestServer(t)
	rec := post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)
	cookie := sessionFrom(t, rec)

	hostile := map[string]string{"Origin": "http://evil.example"}
	if rec := post(t, h, "/api/protected", `{}`, cookie, hostile); rec.Code != http.StatusForbidden {
		t.Errorf("a cross-origin write was allowed: %d", rec.Code)
	}
	// A read is safe and must still work, or the interface breaks on navigation.
	req := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != http.StatusOK {
		t.Errorf("a cross-origin read was refused: %d", out.Code)
	}
}

func TestSameOriginWriteAllowed(t *testing.T) {
	_, h := newTestServer(t)
	rec := post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)
	cookie := sessionFrom(t, rec)

	// httptest requests carry Host "example.com".
	ours := map[string]string{"Origin": "http://example.com"}
	if rec := post(t, h, "/api/protected", `{}`, cookie, ours); rec.Code != http.StatusOK {
		t.Errorf("our own interface was refused: %d %s", rec.Code, rec.Body.String())
	}
}

// TestSessionCookieIsHardened locks the flags down: a session readable by
// script, or sent to another site, is not a session.
func TestSessionCookieIsHardened(t *testing.T) {
	_, h := newTestServer(t)
	rec := post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)
	cookie := sessionFrom(t, rec)

	if !cookie.HttpOnly {
		t.Error("session cookie is readable by script")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("session cookie path = %q, want /", cookie.Path)
	}
}

// TestStaticAssetsStayPublic: the interface must load in order to render a
// sign-in form.
func TestStaticAssetsStayPublic(t *testing.T) {
	_, h := newTestServer(t)
	post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Error("static assets are gated, so the sign-in page can never load")
	}
}

func TestAuthStatusShapes(t *testing.T) {
	_, h := newTestServer(t)

	rec := get(t, h, "/api/auth/status", nil)
	if !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("fresh node status = %s", rec.Body.String())
	}
	setup := post(t, h, "/api/auth/setup", `{"username":"hugo","password":"a-long-enough-password"}`, nil, nil)
	cookie := sessionFrom(t, setup)

	rec = get(t, h, "/api/auth/status", cookie)
	body := rec.Body.String()
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"authenticated":true`) {
		t.Errorf("signed-in status = %s", body)
	}
	if strings.Contains(body, "password_hash") {
		t.Error("the status response leaks the password hash")
	}
}
