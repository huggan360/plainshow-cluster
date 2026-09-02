package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControllerHasNoNodeSurface(t *testing.T) {
	server := NewServer(&Config{ID: "controller", Name: "online"}, "fingerprint")
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"controller"`) {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(`{"command":"true"}`))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("controller exposed a node job route: %d", response.Code)
	}
}
