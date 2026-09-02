package accountserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestRegisterLoginAndAdminDashboard(t *testing.T) {
	store := openTestStore(t)
	if err := store.InitialiseBootstrap(TokenHash("bootstrap")); err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": {Data: []byte("admin")}}
	server := NewServer(&Config{RegistrationOpen: true}, store, web).Handler()

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(
		`{"username":"hugo","display_name":"Hugo","password":"a-long-enough-password","bootstrap_token":"bootstrap"}`))
	register.Header.Set("Content-Type", "application/json")
	registered := httptest.NewRecorder()
	server.ServeHTTP(registered, register)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", registered.Code, registered.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(registered.Body.Bytes(), &session); err != nil || session.Token == "" {
		t.Fatalf("session = %#v, %v", session, err)
	}

	statsRequest := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	statsRequest.Header.Set("Authorization", "Bearer "+session.Token)
	stats := httptest.NewRecorder()
	server.ServeHTTP(stats, statsRequest)
	if stats.Code != http.StatusOK || !strings.Contains(stats.Body.String(), `"accounts":1`) {
		t.Fatalf("stats = %d %s", stats.Code, stats.Body.String())
	}

	badLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(
		`{"username":"hugo","password":"the-wrong-password"}`))
	bad := httptest.NewRecorder()
	server.ServeHTTP(bad, badLogin)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password accepted: %d", bad.Code)
	}
}

func TestOrdinaryAccountCannotReadGlobalDirectory(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("bootstrap"))
	if err := store.CreateAccount(Account{ID: "admin", Username: "admin", DisplayName: "Admin", PasswordHash: "hash"}, TokenHash("bootstrap"), true); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(Account{ID: "member", Username: "member", DisplayName: "Member", PasswordHash: "hash"}, "", true); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession("member-token", "member", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	server := NewServer(&Config{}, store,
		fstest.MapFS{"index.html": {Data: []byte("admin")}}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	request.Header.Set("Authorization", "Bearer member-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("ordinary account read global stats: %d", response.Code)
	}
}
