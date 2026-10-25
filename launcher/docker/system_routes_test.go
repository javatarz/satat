package docker

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestSystemRoutesAreReadOnly is a tripwire for the SYSTEM: 1 flag on the Docker socket proxy
// in compose.yaml (ADR 0027). The proxy filters by path prefix and a global POST flag, so with
// SYSTEM and POST both on, the only thing keeping /system read-only is that the daemon has
// no write route under it. This test talks to the real daemon and fails if that changes.
func TestSystemRoutesAreReadOnly(t *testing.T) {
	api := dockerAPI(t)
	dial := api.Dialer()
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(hc.CloseIdleConnections)

	do := func(t *testing.T, method, path string) int {
		t.Helper()
		req, err := http.NewRequest(method, "http://docker"+path, nil)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	const hint = "a daemon upgrade added a route under /system: revisit the SYSTEM: 1 proxy flag in compose.yaml per ADR 0027"

	if got := do(t, http.MethodGet, "/system/df"); got != http.StatusOK {
		t.Errorf("GET /system/df = %d, want 200 (the cache reaper needs it); %s", got, hint)
	}

	for _, path := range []string{"/system/df", "/system/prune", "/system/info", "/system/events", "/system", "/system/"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			t.Run(method+" "+path, func(t *testing.T) {
				if got := do(t, method, path); got != http.StatusNotFound {
					t.Errorf("%s %s = %d, want 404: %s", method, path, got, hint)
				}
			})
		}
	}
}
