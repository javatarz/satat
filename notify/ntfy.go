package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ntfy publishes one message per event to an ntfy topic, for a phone (ADR 0005). Anyone who knows
// the topic can read and post to it, so Topic is a secret (ADR 0019). Tapping a message opens the
// PR, or else the issue.
type Ntfy struct {
	// Server is the ntfy server's URL, such as https://ntfy.sh.
	Server string
	Topic  string
	// HTTP defaults to a 15 s client.
	HTTP *http.Client
}

// Notify implements Notifier.
func (n *Ntfy) Notify(ctx context.Context, ev Event) error {
	if n.Server == "" || n.Topic == "" {
		return nil
	}
	msg := map[string]any{
		"topic":    n.Topic,
		"title":    title(ev),
		"message":  Text(ev),
		"tags":     []string{tag(ev.State)},
		"priority": priority(ev.State),
	}
	switch {
	case ev.PRURL != "":
		msg["click"] = ev.PRURL
	case ev.Repo != "" && ev.Issue > 0:
		msg["click"] = fmt.Sprintf("https://github.com/%s/issues/%d", ev.Repo, ev.Issue)
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	// The JSON form publishes to the server root, so the title needs no header encoding.
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.Server, "/"), bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	hc := n.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(r)
	if err != nil {
		// The URL holds no secret (the topic is in the body), so the error can be logged as is.
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("ntfy: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func title(ev Event) string {
	if ev.Repo == "" {
		return fmt.Sprintf("Satat: %s", ev.State)
	}
	return fmt.Sprintf("Satat %s#%d: %s", ev.Repo, ev.Issue, ev.State)
}

// tag is the emoji short code ntfy shows for a state.
func tag(s State) string {
	switch s {
	case Published, Completed:
		return "white_check_mark"
	case Failed, NotPublished:
		return "x"
	case WorkerDown:
		return "rotating_light"
	case WorkerUp:
		return "white_check_mark"
	case RateLimited:
		return "hourglass"
	case Skipped:
		return "fast_forward"
	default:
		return "gear"
	}
}

// priority keeps the outcome loud and the steps on the way quiet (ntfy: 1 min to 5 max, 3 default).
func priority(s State) int {
	switch s {
	case WorkerDown:
		return 5
	case Failed, NotPublished, Published, WorkerUp:
		return 4
	case Queued, Started, Completed, Skipped, RateLimited:
		return 3
	default:
		return 2
	}
}
