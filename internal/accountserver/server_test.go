package accountserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
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

func TestEnterpriseNetworkRegistrationAndCollaborationRelay(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("bootstrap"))
	if err := store.CreateAccount(Account{ID: "owner", Username: "owner", DisplayName: "Owner", PasswordHash: "hash"}, TokenHash("bootstrap"), true); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(Account{ID: "member", Username: "member", DisplayName: "Member", PasswordHash: "hash"}, "", true); err != nil {
		t.Fatal(err)
	}
	_ = store.CreateSession("owner-token", "owner", time.Now().Add(time.Hour))
	_ = store.CreateSession("member-token", "member", time.Now().Add(time.Hour))
	server := NewServer(&Config{PublicURL: "https://clusteradmin.example"}, store,
		fstest.MapFS{"index.html": {Data: []byte("admin")}})
	handler := server.Handler()
	key := "0123456789012345678901234567890123456789"

	sync := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/networks/sync", strings.NewReader(
			`{"id":"network","name":"Lab","management_key":"`+key+`","role":"member"}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	ownerSync := sync("owner-token")
	if ownerSync.Code != http.StatusOK || !strings.Contains(ownerSync.Body.String(), "plainshow-enterprise") {
		t.Fatalf("owner sync = %d %s", ownerSync.Code, ownerSync.Body.String())
	}
	if response := sync("member-token"); response.Code != http.StatusForbidden {
		t.Fatalf("uninvited sync = %d %s", response.Code, response.Body.String())
	}
	grant := httptest.NewRequest(http.MethodPost, "/api/networks/network/members", strings.NewReader(
		`{"management_key":"`+key+`","account_id":"member","role":"member"}`))
	grant.Header.Set("Authorization", "Bearer owner-token")
	grant.Header.Set("Content-Type", "application/json")
	granted := httptest.NewRecorder()
	handler.ServeHTTP(granted, grant)
	if granted.Code != http.StatusCreated {
		t.Fatalf("grant = %d %s", granted.Code, granted.Body.String())
	}
	if response := sync("member-token"); response.Code != http.StatusOK {
		t.Fatalf("member sync = %d %s", response.Code, response.Body.String())
	}

	collabToken, err := store.NetworkCollabToken("network")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	dialer := websocket.Dialer{Subprotocols: []string{"plainshow." + collabToken}}
	address := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?network=network"
	first, _, err := dialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, _, err := dialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.WriteMessage(websocket.TextMessage, []byte(`{"operation":"change"}`)); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, message, err := second.ReadMessage()
	if err != nil || string(message) != `{"operation":"change"}` {
		t.Fatalf("relay = %q, %v", message, err)
	}
}
