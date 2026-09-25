// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify sends a person a message when their migration finishes or
// stops. It talks to ntfy, the same push service the rest of the homelab uses,
// so there is one place notifications come from and one topic to subscribe to.
//
// It is deliberately best-effort: a notification that fails must never fail
// the migration it describes. The migration is the work; the message is a
// courtesy on top of it.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// Notifier sends one message. The runner depends on this interface, not on the
// ntfy implementation, so tests do not need a network.
type Notifier interface {
	Notify(ctx context.Context, n Message) error
}

// Message is one notification.
type Message struct {
	// Title is the short line a phone shows first.
	Title string
	// Body is the detail, shown when the message is opened.
	Body string
	// Priority is ntfy's scale: 1 min, 3 default, 4 high, 5 urgent. A failure
	// is high so it stands out; a success is default.
	Priority int
	// Tags are ntfy's emoji shortcodes.
	Tags []string
}

// Client posts to one ntfy topic.
type Client struct {
	base  string
	topic string
	token core.Secret
	http  *http.Client
}

// New builds a client for a topic on an ntfy server. base is the server URL
// (e.g. http://ntfy), topic the topic name. A token is optional: the homelab
// ntfy uses deny-all by default, so in practice one is set.
func New(base, topic string, token core.Secret, timeout time.Duration) (*Client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	topic = strings.TrimSpace(topic)
	if base == "" || topic == "" {
		return nil, errors.New("ntfy needs both a server URL and a topic")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("the ntfy URL is not valid: %w", err)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{base: base, topic: topic, token: token, http: &http.Client{Timeout: timeout}}, nil
}

// Notify posts the message. The body goes as the request body, the rest as
// headers, which is how ntfy's publish API works.
func (c *Client) Notify(ctx context.Context, m Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/"+url.PathEscape(c.topic), strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	if m.Title != "" {
		// ntfy takes the title in a header; non-ASCII must be encoded, so
		// encode it rather than letting the header carry raw bytes.
		req.Header.Set("Title", encodeHeader(m.Title))
	}
	if m.Priority > 0 {
		req.Header.Set("Priority", fmt.Sprintf("%d", m.Priority))
	}
	if len(m.Tags) > 0 {
		req.Header.Set("Tags", strings.Join(m.Tags, ","))
	}
	if !c.token.Empty() {
		req.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("posting the notification: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("the notification was refused with status %d", resp.StatusCode)
	}
	return nil
}

// encodeHeader makes a header value safe for HTTP. ntfy expects UTF-8 in
// headers, which Go allows, but a value with a newline would be a header
// injection; strip control characters so a title built from user data cannot
// break out of its header.
func encodeHeader(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\r' || r == '\n' || r == 0 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
