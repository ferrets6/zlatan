// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
)

func TestNewRefusesMissingParts(t *testing.T) {
	if _, err := New("", "topic", "", 0); err == nil {
		t.Error("New should refuse an empty server URL")
	}
	if _, err := New("http://ntfy", "", "", 0); err == nil {
		t.Error("New should refuse an empty topic")
	}
}

func TestNotifyPostsTitleBodyAndToken(t *testing.T) {
	var got struct {
		path, title, priority, tags, auth, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.title = r.Header.Get("Title")
		got.priority = r.Header.Get("Priority")
		got.tags = r.Header.Get("Tags")
		got.auth = r.Header.Get("Authorization")
		got.body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "migrations", core.Secret("tok"), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Notify(context.Background(), Message{
		Title:    "zlatan: your files are in Nextcloud",
		Body:     "The copy finished and was checked.",
		Priority: 4,
		Tags:     []string{"white_check_mark"},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if got.path != "/migrations" {
		t.Errorf("path = %q, want /migrations", got.path)
	}
	if got.title != "zlatan: your files are in Nextcloud" {
		t.Errorf("title = %q", got.title)
	}
	if got.priority != "4" {
		t.Errorf("priority = %q, want 4", got.priority)
	}
	if got.tags != "white_check_mark" {
		t.Errorf("tags = %q", got.tags)
	}
	if got.auth != "Bearer tok" {
		t.Errorf("authorization = %q, want Bearer tok", got.auth)
	}
	if got.body != "The copy finished and was checked." {
		t.Errorf("body = %q", got.body)
	}
}

func TestNotifyRefusesANonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "migrations", "", 0)
	if err := c.Notify(context.Background(), Message{Title: "x"}); err == nil {
		t.Error("Notify should fail on a non-2xx response")
	}
}

func TestEncodeHeaderStripsNewlines(t *testing.T) {
	got := encodeHeader("hello\r\nInjected: yes\x00")
	if got != "helloInjected: yes" {
		t.Errorf("encodeHeader = %q, want the control characters stripped", got)
	}
}
