// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretNeverPrintsItsValue(t *testing.T) {
	s := Secret("super-secret-token")

	if got := s.String(); strings.Contains(got, "super-secret-token") {
		t.Fatalf("String leaked the value: %q", got)
	}
	if got := s.Reveal(); got != "super-secret-token" {
		t.Fatalf("Reveal returned %q", got)
	}

	// A struct carrying a Secret must not leak it through %v or JSON.
	wrapped := struct {
		Token Secret `json:"token"`
	}{Token: s}
	if got := strings.Contains(strings.Join([]string{
		jsonOf(t, wrapped),
	}, " "), "super-secret-token"); got {
		t.Fatal("JSON encoding leaked the value")
	}
}

func TestSecretEmpty(t *testing.T) {
	if !Secret("").Empty() {
		t.Fatal("empty secret reported as set")
	}
	if Secret("x").Empty() {
		t.Fatal("set secret reported as empty")
	}
	if Secret("").String() != "<unset>" {
		t.Fatal("unset secret should say so")
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestTrackTerminal(t *testing.T) {
	cases := []struct {
		state DriveState
		want  bool
	}{
		{DriveDone, true},
		{DriveFailed, true},
		{DriveCancelled, true},
		{DriveCopying, false},
		{DriveNotStarted, false},
	}
	for _, c := range cases {
		if got := c.state.Terminal(); got != c.want {
			t.Errorf("DriveState(%q).Terminal() = %v, want %v", c.state, got, c.want)
		}
	}

	photos := []struct {
		state PhotosState
		want  bool
	}{
		{PhotosDone, true},
		{PhotosAwaitingShare, false},
		{PhotosImporting, false},
	}
	for _, c := range photos {
		if got := c.state.Terminal(); got != c.want {
			t.Errorf("PhotosState(%q).Terminal() = %v, want %v", c.state, got, c.want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://nextcloud", "http://nextcloud"},
		{"http://user:pass@nextcloud/x", "http://***@nextcloud/x"},
		{"https://admin:hunter2@cloud.example.com/path", "https://***@cloud.example.com/path"},
		{"", ""},
	}
	for _, c := range cases {
		if got := RedactURL(c.in); got != c.want {
			t.Errorf("RedactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSummaries(t *testing.T) {
	m := Migration{
		DriveState:     DriveDone,
		PhotosState:    PhotosAwaitingShare,
		DriveProgress:  "copied 1.2 GiB",
		PhotosProgress: "waiting for the Takeout",
	}
	s := m.Summaries()
	if len(s) != 2 {
		t.Fatalf("want 2 summaries, got %d", len(s))
	}
	if !s[0].Done || s[0].Failed {
		t.Errorf("drive summary wrong: %+v", s[0])
	}
	if s[1].Done || s[1].Failed {
		t.Errorf("photos summary wrong: %+v", s[1])
	}
}
