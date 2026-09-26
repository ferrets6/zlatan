// SPDX-License-Identifier: AGPL-3.0-or-later

// Package immich talks to Immich's own API, only to prove that an API key
// belongs to the person who pasted it.
//
// Why the key is the person's own: Immich has no admin endpoint that mints an
// API key for another account. `POST /api-keys` always uses the authenticated
// user's id (server/src/services/api-key.service.ts), and the admin routes
// expose users, sessions and preferences but never a key. So a key can only
// come from the person, and the import runs as them. That is also the better
// security answer: the key can do nothing the person could not do themselves,
// and one person's import can never touch another's library.
package immich

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

// Provider is the key under which the sealed API key is stored.
const Provider = "immich"

// Credentials is one person's Immich API key, plus the account it belongs to
// so the wizard can show whose it is. It is JSON-sealed as a whole; String
// redacts the key, so a stray log line cannot leak it.
type Credentials struct {
	APIKey string `json:"apiKey"`
	Email  string `json:"email"`
}

// String redacts the key, so logging a Credentials by accident is safe.
func (c Credentials) String() string {
	return fmt.Sprintf("immich credentials for %s (key redacted)", c.Email)
}

// Me is the part of Immich's own user record the wizard needs: whose key this
// is, so the person can confirm they pasted their own and not somebody else's.
type Me struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
}

// Client is an Immich instance reached over the internal Docker network.
type Client struct {
	base string
	http *http.Client
}

// New builds the client. base is the internal URL (e.g. http://immich_server:2283).
func New(base string, timeout time.Duration) (*Client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, errors.New("the Immich URL is empty")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("the Immich URL is not valid: %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{base: base, http: &http.Client{Timeout: timeout}}, nil
}

// Validate asks Immich who the key belongs to. It is the whole of the check:
// a key that Immich accepts and that names an account is a working key, and
// the account it names is the one every asset will be filed under. A key
// Immich rejects comes back as an error, so a mistyped paste is caught here
// rather than hours into an import.
func (c *Client) Validate(ctx context.Context, apiKey string) (Me, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Me{}, errors.New("the Immich API key is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/users/me", nil)
	if err != nil {
		return Me{}, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Me{}, fmt.Errorf("checking the Immich API key: %w", err)
	}
	defer drain(resp.Body)

	// 401 is the honest answer for a wrong key; anything else non-200 is a
	// server problem, not the person's mistake, and says so.
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Me{}, errors.New("Immich did not accept that API key")
	default:
		return Me{}, fmt.Errorf("checking the Immich API key: unexpected status %d", resp.StatusCode)
	}

	var me Me
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return Me{}, fmt.Errorf("reading the Immich account: %w", err)
	}
	if me.Email == "" {
		// A key that names no account is not usable for an import: Immich
		// files every asset under the key's owner, and there would be no owner
		// to file under or to show the person.
		return Me{}, errors.New("Immich accepted the key but returned no account")
	}
	return me, nil
}

func drain(r io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 1<<16))
	_ = r.Close()
}

// Sealer encrypts the credentials at rest. It is the same interface the rest
// of the service seals with, so the key lives under the one token key and not
// a second secret to rotate.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// SealCredentials encodes and seals the API key, so only ciphertext reaches
// the database.
func SealCredentials(s Sealer, c Credentials) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encoding the Immich credentials: %w", err)
	}
	return s.Seal(raw)
}

// OpenCredentials unseals and decodes the API key.
func OpenCredentials(s Sealer, sealed []byte) (Credentials, error) {
	raw, err := s.Open(sealed)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credentials{}, fmt.Errorf("decoding the Immich credentials: %w", err)
	}
	return c, nil
}
