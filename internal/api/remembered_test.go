package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// signedInMachine is a node that holds a valid account credential — the state a
// desktop is in every time its window is closed and reopened.
func signedInMachine(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	account := store.Account{ID: "acct-1", Username: "huggan360", Created: store.Now()}
	if err := srv.store.UpsertAccount(account); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Account.ID = account.ID
	srv.cfg.Account.Username = account.Username
	srv.cfg.Account.Server = "https://clusteradmin.example"
	srv.cfg.Network.Bind = "127.0.0.1"
	srv.cfg.Auth.RememberThisMachine = true
	if err := config.SaveAccountToken(srv.layout, "a-valid-account-token"); err != nil {
		t.Fatal(err)
	}
	return srv
}

func loopbackRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	return r
}

// TestClosingTheWindowDoesNotSignYouOut is the whole point. Wails uses
// WebKitGTK's default web context, whose cookie store is memory-only, so the
// session cookie dies with the window and no option changes that. The durable
// credential is the node's own account token.
func TestClosingTheWindowDoesNotSignYouOut(t *testing.T) {
	srv := signedInMachine(t)
	account, ok := srv.rememberedAccount(loopbackRequest())
	if !ok {
		t.Fatal("a signed-in machine refused to re-establish its own session")
	}
	if account.Username != "huggan360" {
		t.Fatalf("re-established as %q, want huggan360", account.Username)
	}
}

func TestAuthStatusRestoresRememberedDesktopSession(t *testing.T) {
	srv := signedInMachine(t)
	recorder := httptest.NewRecorder()
	srv.authStatus(recorder, loopbackRequest())
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth status returned %d", recorder.Code)
	}
	var response struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Authenticated {
		t.Fatal("startup status did not restore the remembered session")
	}
	if recorder.Header().Get("Set-Cookie") == "" {
		t.Fatal("startup status restored no browser cookie")
	}
}

func TestRememberedSessionRecoversExpiredCookie(t *testing.T) {
	for _, status := range []bool{false, true} {
		srv := signedInMachine(t)
		r := loopbackRequest()
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired-session"})
		w := httptest.NewRecorder()
		if status {
			srv.authStatus(w, r)
			if !strings.Contains(w.Body.String(), `"authenticated":true`) {
				t.Fatalf("remembered status: %s", w.Body)
			}
		} else {
			srv.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(w, r)
		}
		if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") == "" {
			t.Fatalf("status=%v: code=%d cookies=%v", status, w.Code, w.Result().Cookies())
		}
	}
}

func TestRememberedSessionCannotBypassOriginGuard(t *testing.T) {
	srv := signedInMachine(t)
	r := loopbackRequest()
	r.Method = http.MethodPost
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	srv.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("cross-origin mutation reached the handler")
	})).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("code=%d cookies=%v", w.Code, w.Result().Cookies())
	}
}

// TestRememberIsRefusedOffLoopback: once the interface answers on the network,
// "local" no longer means "the person at the keyboard".
func TestRememberIsRefusedOffLoopback(t *testing.T) {
	srv := signedInMachine(t)
	srv.cfg.Network.Bind = "0.0.0.0"
	if _, ok := srv.rememberedAccount(loopbackRequest()); ok {
		t.Fatal("a network-bound node handed out a session without a password")
	}
}

func TestRememberIsRefusedForARemoteCaller(t *testing.T) {
	srv := signedInMachine(t)
	r := loopbackRequest()
	r.RemoteAddr = "10.0.0.9:41000"
	if _, ok := srv.rememberedAccount(r); ok {
		t.Fatal("a remote caller was handed a session without a password")
	}
}

func TestRememberIsRefusedWhenTurnedOff(t *testing.T) {
	srv := signedInMachine(t)
	srv.cfg.Auth.RememberThisMachine = false
	if _, ok := srv.rememberedAccount(loopbackRequest()); ok {
		t.Fatal("the setting was off and a session was issued anyway")
	}
}

// TestSigningOutActuallySignsOut is the trap this feature creates. While the
// node keeps an account credential it can hand itself a fresh session, so
// clearing the cookie alone would make the sign-out button do nothing.
func TestSigningOutActuallySignsOut(t *testing.T) {
	srv := signedInMachine(t)
	if _, ok := srv.rememberedAccount(loopbackRequest()); !ok {
		t.Fatal("precondition: the machine should start signed in")
	}

	recorder := httptest.NewRecorder()
	srv.authLogout(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout returned %d", recorder.Code)
	}

	if _, ok := srv.rememberedAccount(loopbackRequest()); ok {
		t.Fatal("signing out left a credential that immediately signs back in")
	}
}

func TestLoopbackBindRecognisesTheUsualSpellings(t *testing.T) {
	for _, bind := range []string{"", "localhost", "127.0.0.1", "::1"} {
		if !loopbackBind(bind) {
			t.Errorf("loopbackBind(%q) = false, want true", bind)
		}
	}
	for _, bind := range []string{"0.0.0.0", "192.168.1.4", "::"} {
		if loopbackBind(bind) {
			t.Errorf("loopbackBind(%q) = true, want false", bind)
		}
	}
}
