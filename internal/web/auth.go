// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// ErrUnauthenticated means the request carries no identity Zlatan is willing
// to believe.
var ErrUnauthenticated = errors.New("no authenticated user")

// identityFrom resolves who is making the request. The identity comes from the
// forward-auth header the proxy sets, and is believed only when the request
// arrives from the configured proxy network: without that check, any container
// sharing the Docker network could claim to be somebody else and read their
// migration.
//
// It is never taken from a URL parameter or a cookie. A person can therefore
// only ever see their own migration, by construction.
func identityFrom(r *http.Request, trustedProxy string) (user, email string, err error) {
	if !fromTrustedProxy(r, trustedProxy) {
		return "", "", ErrUnauthenticated
	}
	user = strings.TrimSpace(r.Header.Get("Remote-User"))
	if user == "" {
		return "", "", ErrUnauthenticated
	}
	return user, strings.TrimSpace(r.Header.Get("Remote-Email")), nil
}

// fromTrustedProxy reports whether the request arrived from the configured
// network. An empty setting trusts nothing, so a header is never believed by
// accident.
func fromTrustedProxy(r *http.Request, trusted string) bool {
	if strings.TrimSpace(trusted) == "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, part := range strings.Split(trusted, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(part); err == nil {
			if network.Contains(ip) {
				return true
			}
			continue
		}
		if single := net.ParseIP(part); single != nil && single.Equal(ip) {
			return true
		}
	}
	return false
}

// proxyGate answers only requests carrying the secret the proxy injects, so a
// container sharing the Docker network cannot reach Zlatan directly and skip
// the SSO in front of the public name. With no secret configured it is a
// no-op, which is why a public bind without one is refused at config load.
func proxyGate(secret core.Secret, next http.Handler) http.Handler {
	if secret.Empty() {
		return next
	}
	expected := sha256.Sum256([]byte(secret.Reveal()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := sha256.Sum256([]byte(r.Header.Get("X-Zlatan-Proxy-Secret")))
		if subtle.ConstantTimeCompare(expected[:], presented[:]) != 1 {
			http.Error(w, "this service is reachable only through the proxy", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
