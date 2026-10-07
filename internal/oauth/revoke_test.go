// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The refresh token goes to Google's revoke endpoint as a form value. A token
// Google calls invalid is already revoked or expired: that is done, not an
// error. Any other refusal is reported.
func TestRevoke(t *testing.T) {
	cases := []struct {
		status  int
		body    string
		wantErr bool
	}{
		{http.StatusOK, "", false},
		{http.StatusBadRequest, `{"error":"invalid_token"}`, false},
		{http.StatusBadRequest, `{"error":"unsupported_token_type"}`, true},
		{http.StatusServiceUnavailable, "", true},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil || r.Method != http.MethodPost || r.PostForm.Get("token") != "1//refresh" {
				t.Errorf("unexpected request %s, token %q", r.Method, r.PostForm.Get("token"))
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		revokeURL = srv.URL
		p, err := New("id", "secret", "https://zlatan.example/cb")
		if err != nil {
			t.Fatal(err)
		}
		err = p.Revoke(context.Background(), "1//refresh")
		if (err != nil) != c.wantErr {
			t.Errorf("status %d %s: err = %v, wantErr %v", c.status, c.body, err, c.wantErr)
		}
		srv.Close()
	}
}
