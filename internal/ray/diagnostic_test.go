package ray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiagnosticReturnsWorkerProofAndCleansUp(t *testing.T) {
	var stopped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/jobs/":
			w.Write([]byte(`{"submission_id":"test"}`))
		case "/api/jobs/test":
			w.Write([]byte(`{"status":"SUCCEEDED"}`))
		case "/api/jobs/test/logs":
			w.Write([]byte(`{"logs":"PLAINSHOW_TEST=[{\"address\":\"100.64.0.2\",\"hostname\":\"worker\",\"ok\":true,\"resources\":{\"CPU\":4}}]\n"}`))
		case "/api/jobs/test/stop":
			stopped.Store(true)
			w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results, err := Diagnose(ctx, server.URL, "test")
	if err != nil || len(results) != 1 || !results[0].OK {
		t.Fatalf("%v %v", results, err)
	}
	if !stopped.Load() {
		t.Fatal("job was not cleaned up")
	}
}
