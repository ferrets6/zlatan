// SPDX-License-Identifier: AGPL-3.0-or-later

package nextcloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The app password deletes itself: the request authenticates with it, on the
// OCS endpoint, with the header Nextcloud requires of every OCS call. A 401
// means it is already gone, which is the outcome asked for.
func TestRevokeAppPassword(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, _ := r.BasicAuth()
			if r.Method != http.MethodDelete || r.URL.Path != "/ocs/v2.php/core/apppassword" ||
				r.Header.Get("OCS-APIRequest") != "true" || user != "marco" || pass != "app-pw" {
				t.Errorf("unexpected request %s %s (user %q)", r.Method, r.URL.Path, user)
			}
			w.WriteHeader(status)
		}))
		c, _ := New(srv.URL, 0)
		if err := c.RevokeAppPassword(context.Background(), Credentials{LoginName: "marco", AppPassword: "app-pw"}); err != nil {
			t.Errorf("status %d: %v", status, err)
		}
		srv.Close()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, 0)
	if err := c.RevokeAppPassword(context.Background(), Credentials{LoginName: "marco", AppPassword: "app-pw"}); err == nil {
		t.Error("a 500 must be reported, not taken as done")
	}
}
