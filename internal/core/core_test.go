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
		{PhotosAwaitingTakeout, false},
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

func TestSafeNameRejectsTraversal(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"marco", "marco"},
		{"../../etc/passwd", "etc_passwd"},
		{"a/b", "a_b"},
		{"..", "unknown"},
		{"", "unknown"},
		{".hidden", "hidden"},
		{"user@example.com", "user_example_com"},
		{"a..b", "a__b"},
		{"...", "unknown"},
	}
	for _, c := range cases {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A sanitized identity must never escape a staging root.
func TestSafeNameStaysUnderStaging(t *testing.T) {
	base := "/staging"
	for _, hostile := range []string{"../../etc", "..", "/etc/passwd", "a/../../b", "....//...."} {
		got := base + "/" + SafeName(hostile)
		if !strings.HasPrefix(got, base+"/") || strings.Contains(got, "..") {
			t.Errorf("SafeName(%q) escaped the staging root: %s", hostile, got)
		}
	}
}

func TestSummaries(t *testing.T) {
	m := Migration{
		DriveState:     DriveDone,
		PhotosState:    PhotosAwaitingTakeout,
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

func TestQuotaOverrun(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	cases := []struct {
		name    string
		m       Migration
		budget  int
		wantAny bool
	}{
		{
			name:   "well under budget",
			m:      Migration{DriveSourceBytes: 10 * gib, QuotaUsedBytes: 5 * gib},
			budget: 100,
		},
		{
			name:    "over budget",
			m:       Migration{DriveSourceBytes: 60 * gib, QuotaUsedBytes: 50 * gib},
			budget:  100,
			wantAny: true,
		},
		{
			name:   "exactly at budget is not a warning",
			m:      Migration{DriveSourceBytes: 50 * gib, QuotaUsedBytes: 50 * gib},
			budget: 100,
		},
		{
			name:   "unknown source size never warns",
			m:      Migration{DriveSourceBytes: 0, QuotaUsedBytes: 200 * gib},
			budget: 100,
		},
		{
			name:    "admin budget of 200 leaves more room",
			m:       Migration{DriveSourceBytes: 190 * gib, QuotaUsedBytes: 20 * gib},
			budget:  200,
			wantAny: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			projected, budget, over := c.m.QuotaOverrun(c.budget)
			if over != c.wantAny {
				t.Errorf("QuotaOverrun(%d) over = %v, want %v", c.budget, over, c.wantAny)
			}
			if over && projected <= budget {
				t.Errorf("QuotaOverrun(%d) says over but %d <= %d", c.budget, projected, budget)
			}
		})
	}
}

// A progress with no arguments is stored as the bare key, so the common case
// stays readable in the database and in a log line.
func TestProgressRoundTripsWithoutArguments(t *testing.T) {
	p := Progress{Key: ProgressPreparing}
	if got := EncodeProgress(p); got != ProgressPreparing {
		t.Fatalf("EncodeProgress = %q, want the bare key", got)
	}
	if got := DecodeProgress(ProgressPreparing); got.Key != ProgressPreparing || len(got.Args) != 0 {
		t.Fatalf("DecodeProgress = %+v, want the key and no args", got)
	}
}

// A progress with arguments carries the numbers as numbers, so the web layer
// can format them in the reader's language rather than the server's.
func TestProgressRoundTripsWithArguments(t *testing.T) {
	p := Progress{Key: ProgressCopying, Args: []int64{44_181_000_000, 12480}}
	encoded := EncodeProgress(p)
	if !strings.HasPrefix(encoded, "{") {
		t.Fatalf("a progress with arguments must be JSON, got %q", encoded)
	}
	got := DecodeProgress(encoded)
	if got.Key != p.Key || len(got.Args) != 2 || got.Args[0] != p.Args[0] || got.Args[1] != p.Args[1] {
		t.Fatalf("DecodeProgress(EncodeProgress(p)) = %+v, want %+v", got, p)
	}
}

// A line written before the keys existed, or by hand, must not blank the slot:
// it is shown as it is.
func TestDecodeProgressKeepsAPlainSentence(t *testing.T) {
	got := DecodeProgress("some older sentence")
	if got.Key != "some older sentence" || len(got.Args) != 0 {
		t.Fatalf("DecodeProgress = %+v, want the sentence as the key", got)
	}
	if DecodeProgress("").Key != "" {
		t.Error("an empty value must decode to an empty progress")
	}
}

// The Drive route is read from one place, whichever Photos progress carries it.
func TestProgressFromDrive(t *testing.T) {
	for _, c := range []struct {
		p    Progress
		want bool
	}{
		{Progress{Key: ProgressPhotosDoneDrive}, true},
		{Progress{Key: ProgressPhotosDone}, false},
		{ImportErrors(0, 1, true), true},
		{ImportErrors(0, 1, false), false},
		{PhotosDoneAccepted(true), true},
		{PhotosDoneAccepted(false), false},
		{Progress{Key: FailImportErrors, Args: []int64{0, 1}}, false}, // stored before the flag existed
	} {
		if got := c.p.FromDrive(); got != c.want {
			t.Errorf("%+v.FromDrive() = %v, want %v", c.p, got, c.want)
		}
	}
}

// Retrying the files named stands for retrying all of it only when the list is
// complete and at least one of them can still be found.
func TestCanRetryProblems(t *testing.T) {
	one := []Problem{{File: "a"}}
	for _, c := range []struct {
		v    Verify
		want bool
	}{
		{Verify{Mismatch: 1, Problems: one}, true},
		{Verify{Mismatch: 2, Problems: one}, false},                                   // cut short
		{Verify{Mismatch: 1}, false},                                                  // none named
		{Verify{Mismatch: 1, Problems: []Problem{{File: "a", Missing: true}}}, false}, // nothing to send
	} {
		if got := c.v.CanRetryProblems(); got != c.want {
			t.Errorf("%+v.CanRetryProblems() = %v, want %v", c.v, got, c.want)
		}
	}
}
