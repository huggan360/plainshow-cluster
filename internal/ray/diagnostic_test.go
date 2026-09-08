package ray

import (
	"context"
	"encoding/json"
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
			json.NewEncoder(w).Encode(map[string]string{"logs": `PLAINSHOW_TEST=[{"address":"100.64.0.2","hostname":"worker","ok":true,"resources":{"CPU":4},"cpu":{"name":"CPU","status":"passed","detail":"Arithmetic passed"},"gpu":{"name":"GPU","status":"missing","detail":"PyTorch missing"},"software":[{"name":"CUDA toolkit","status":"available","detail":"12.8"}]}]` + "\n"})
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
	if results[0].CPU == nil || results[0].CPU.Status != "passed" || results[0].GPU == nil || results[0].GPU.Status != "missing" || len(results[0].Software) != 1 {
		t.Fatalf("lost informational software checks: %+v", results[0])
	}
}
