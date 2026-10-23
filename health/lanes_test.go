package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLaneSetupCheck(t *testing.T) {
	var s LaneSetup
	c := s.Check()
	if c.Name != "lanes" {
		t.Fatalf("name = %q, want lanes", c.Name)
	}
	if err := c.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not finished") {
		t.Fatalf("before Done: %v, want a not-finished error", err)
	}
	s.Done(nil)
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("after Done(nil): %v", err)
	}
	s.Done(errors.New("lane claude: image not pinned"))
	if err := c.Run(context.Background()); err == nil || err.Error() != "lane claude: image not pinned" {
		t.Fatalf("after Done(err): %v, want the setup error", err)
	}
}

// A worker whose lane setup failed still answers /readyz, naming the lanes check, so deploy up
// shows why instead of only "restarting".
func TestReadyzNamesFailedLaneSetup(t *testing.T) {
	var s LaneSetup
	c := New(time.Millisecond, s.Check(), ok())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	s.Done(errors.New("lane claude: image is not a registry reference"))

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := httptest.NewRecorder()
		c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var body struct{ Failing map[string]string }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code == 503 && body.Failing["lanes"] == "lane claude: image is not a registry reference" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz = %d %s, want 503 failing lanes", rec.Code, rec.Body)
		}
		time.Sleep(time.Millisecond)
	}
	if err := WaitReady(ctx, c, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "lanes: lane claude") {
		t.Fatalf("WaitReady = %v, want it to name the lanes check", err)
	}
}
