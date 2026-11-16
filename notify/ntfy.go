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
	m := Compose(ev)
	body := m.Body
	if ev.Repo != "" && ev.Issue > 0 {
		// The reader is not on the issue, so name it first.
		issue := fmt.Sprintf("**%s#%d**", ev.Repo, ev.Issue)
		if ev.IssueTitle != "" {
			issue += ": " + ev.IssueTitle
		}
		body = issue + "\n\n" + body
	}
	msg := map[string]any{
		"topic":    n.Topic,
		"title":    "[" + string(m.Level) + "] " + m.Headline,
		"message":  body,
		"markdown": true,
		"priority": m.Priority,
	}
	switch {
	case m.Link != "":
		msg["click"] = m.Link
	case ev.Repo != "" && ev.Issue > 0:
		msg["click"] = fmt.Sprintf("https://github.com/%s/issues/%d", ev.Repo, ev.Issue)
	}
	if ev.Repo != "" && ev.Issue > 0 && m.Link != "" {
		msg["actions"] = []map[string]any{{"action": "view", "label": "Open issue", "url": fmt.Sprintf("https://github.com/%s/issues/%d", ev.Repo, ev.Issue)}}
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
