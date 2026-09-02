package accountclient

import "testing"

func TestAccountServerRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://clusteradmin.example", "ftp://clusteradmin.example", "clusteradmin.example"} {
		if _, err := New(endpoint); err == nil {
			t.Errorf("accepted insecure account server %q", endpoint)
		}
	}
	for _, endpoint := range []string{"https://clusteradmin.example", "http://127.0.0.1:10002"} {
		if _, err := New(endpoint); err != nil {
			t.Errorf("refused account server %q: %v", endpoint, err)
		}
	}
}
