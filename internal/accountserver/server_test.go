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
	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	index := httptest.NewRecorder()
	server.ServeHTTP(index, indexRequest)
	if index.Code != http.StatusOK || index.Body.String() != "admin" {
		t.Fatalf("admin index = %d %q", index.Code, index.Body.String())
	}
	iconRequest := httptest.NewRequest(http.MethodGet, "/brand/plainshow-icon.webp", nil)
	icon := httptest.NewRecorder()
	server.ServeHTTP(icon, iconRequest)
	if icon.Code != http.StatusOK || icon.Header().Get("Content-Type") != "image/webp" || icon.Body.Len() != 12510 {
		t.Fatalf("brand icon = %d %q %d bytes", icon.Code, icon.Header().Get("Content-Type"), icon.Body.Len())
	}

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

func TestControllerRegistryIsSeparateFromAccountServer(t *testing.T) {
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
	if ownerSync.Code != http.StatusOK || !strings.Contains(ownerSync.Body.String(), `"controller":{}`) {
		t.Fatalf("owner sync = %d %s", ownerSync.Code, ownerSync.Body.String())
	}
	configure := httptest.NewRequest(http.MethodPut, "/api/controllers/controller-1", strings.NewReader(
		`{"name":"Main controller","public_url":"https://cluster.example","network_ids":["network"]}`))
	configure.Header.Set("Authorization", "Bearer owner-token")
	configure.Header.Set("Content-Type", "application/json")
	configured := httptest.NewRecorder()
	handler.ServeHTTP(configured, configure)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure controller = %d %s", configured.Code, configured.Body.String())
	}
	var registration ControllerConfiguration
	if err := json.Unmarshal(configured.Body.Bytes(), &registration); err != nil ||
		registration.Credential == "" || len(registration.Networks) != 1 || registration.Networks[0].RelayToken == "" {
		t.Fatalf("controller registration = %+v, %v", registration, err)
	}
	checkIn := httptest.NewRequest(http.MethodPost, "/api/controllers/controller-1/check-in", nil)
	checkIn.Header.Set("Authorization", "Bearer "+registration.Credential)
	checkedIn := httptest.NewRecorder()
	handler.ServeHTTP(checkedIn, checkIn)
	if checkedIn.Code != http.StatusOK || !strings.Contains(checkedIn.Body.String(), `"relay_token"`) {
		t.Fatalf("controller check-in = %d %s", checkedIn.Code, checkedIn.Body.String())
	}
	ownerSync = sync("owner-token")
	if ownerSync.Code != http.StatusOK || !strings.Contains(ownerSync.Body.String(), "https://cluster.example") ||
		!strings.Contains(ownerSync.Body.String(), registration.Networks[0].RelayToken) {
		t.Fatalf("owner controller discovery = %d %s", ownerSync.Code, ownerSync.Body.String())
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
	contextRequest := httptest.NewRequest(http.MethodGet, "/api/controller/context/controller-1", nil)
	contextRequest.Header.Set("Authorization", "Bearer member-token")
	contextResponse := httptest.NewRecorder()
	handler.ServeHTTP(contextResponse, contextRequest)
	if contextResponse.Code != http.StatusOK || !strings.Contains(contextResponse.Body.String(), `"can_access":true`) ||
		!strings.Contains(contextResponse.Body.String(), `"can_manage":false`) {
		t.Fatalf("member controller context = %d %s", contextResponse.Code, contextResponse.Body.String())
	}
	websocketRequest := httptest.NewRequest(http.MethodGet, "/ws?network=network", nil)
	websocketResponse := httptest.NewRecorder()
	handler.ServeHTTP(websocketResponse, websocketRequest)
	if websocketResponse.Code != http.StatusNotFound {
		t.Fatalf("account server still serves collaboration websocket: %d", websocketResponse.Code)
	}
}
