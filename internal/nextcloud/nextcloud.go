// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nextcloud talks to Nextcloud's Login Flow v2, the official way for a
// client to obtain a per-user app password without ever seeing the person's
// password.
//
// Why not the admin account: an admin over WebDAV can read another person's
// files but cannot write into their space (MKCOL answers 403), and the direct
// filesystem route would need the Docker socket — root-equivalent access on
// the host — plus a coupling to Nextcloud's internal layout. The Login Flow
// gives the user's own credential instead, so the import runs with exactly the
// rights the person has, and Zlatan holds nothing more.
package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider is the key under which the sealed app password is stored.
const Provider = "nextcloud"

// Credentials is what the Login Flow returns once the person grants access.
// It is a plain struct (not core.Secret fields) because it is JSON-sealed as a
// whole; the redaction is done by String, so a stray log line cannot leak it.
type Credentials struct {
	Server      string `json:"server"`
	LoginName   string `json:"loginName"`
	AppPassword string `json:"appPassword"`
}

// String redacts the password, so logging a Credentials by accident is safe.
func (c Credentials) String() string {
	return fmt.Sprintf("nextcloud credentials for %s (password redacted)", c.LoginName)
}

// Flow is a started Login Flow: the token the server polls with, and the URL
// the person opens to grant access.
type Flow struct {
	PollToken string
	LoginURL  string
}

// Client is a Nextcloud instance reached over the internal Docker network. The
// login URL it returns is absolute and public (Nextcloud knows its own name),
// so the browser is sent straight to it; the poll is made against the internal
// address, so a grant does not depend on the public path being up.
type Client struct {
	base string
	http *http.Client
}

// New builds the client. base is the internal URL (e.g. http://nextcloud).
func New(base string, timeout time.Duration) (*Client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, errors.New("the Nextcloud URL is empty")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("the Nextcloud URL is not valid: %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{base: base, http: &http.Client{Timeout: timeout}}, nil
}

// DAVURL is the WebDAV endpoint for one person, built from the internal base
// and the account's login name. rclone is pointed here.
func (c *Client) DAVURL(loginName string) string {
	return c.base + "/remote.php/dav/files/" + url.PathEscape(loginName)
}

// BeginFlow starts a Login Flow. It returns the URL to send the person to and
// the token to poll with.
func (c *Client) BeginFlow(ctx context.Context) (Flow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/index.php/login/v2", nil)
	if err != nil {
		return Flow{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Flow{}, fmt.Errorf("starting the Nextcloud login flow: %w", err)
	}
	defer drain(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return Flow{}, fmt.Errorf("starting the Nextcloud login flow: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		Poll struct {
			Token string `json:"token"`
		} `json:"poll"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Flow{}, fmt.Errorf("decoding the login flow: %w", err)
	}
	if payload.Poll.Token == "" || payload.Login == "" {
		return Flow{}, errors.New("the login flow response was incomplete")
	}
	return Flow{PollToken: payload.Poll.Token, LoginURL: payload.Login}, nil
}

// Poll asks whether the person has granted access. done is false while the
// flow is still pending: Nextcloud answers 404 until then, which is the
// documented contract, not an error.
func (c *Client) Poll(ctx context.Context, token string) (creds Credentials, done bool, err error) {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/login/v2/poll", strings.NewReader(form.Encode()))
	if err != nil {
		return Credentials{}, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return Credentials{}, false, fmt.Errorf("polling the Nextcloud login flow: %w", err)
	}
	defer drain(resp.Body)

	switch resp.StatusCode {
	case http.StatusNotFound:
		// Not granted yet (or already completed and consumed).
		return Credentials{}, false, nil
	case http.StatusOK:
		// Fall through.
	default:
		return Credentials{}, false, fmt.Errorf("polling the Nextcloud login flow: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		Server      string `json:"server"`
		LoginName   string `json:"loginName"`
		AppPassword string `json:"appPassword"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Credentials{}, false, fmt.Errorf("decoding the login flow result: %w", err)
	}
	if payload.LoginName == "" || payload.AppPassword == "" {
		return Credentials{}, false, errors.New("the login flow result was incomplete")
	}
	return Credentials{
		Server:      payload.Server,
		LoginName:   payload.LoginName,
		AppPassword: payload.AppPassword,
	}, true, nil
}

// Sealer is the part of core.Sealer this package needs.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// SealCredentials encodes and seals the credentials, so only ciphertext
// reaches the database. The app password grants full access to the person's
// files: it is as sensitive as the Google refresh token.
func SealCredentials(s Sealer, c Credentials) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encoding the Nextcloud credentials: %w", err)
	}
	return s.Seal(raw)
}

// OpenCredentials unseals and decodes the credentials.
func OpenCredentials(s Sealer, sealed []byte) (Credentials, error) {
	raw, err := s.Open(sealed)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credentials{}, fmt.Errorf("decoding the Nextcloud credentials: %w", err)
	}
	return c, nil
}

// drain reads and discards a body so the connection can be reused. Errors are
// deliberately ignored: the response has already been handled.
func drain(r io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 1<<16))
	_ = r.Close()
}
