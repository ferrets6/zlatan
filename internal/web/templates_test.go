// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// The wizard renders exactly one of the design's screens, chosen from the two
// track states. These cases are the mapping the design depends on.
func TestScreenFor(t *testing.T) {
	cases := []struct {
		name   string
		drive  core.DriveState
		photos core.PhotosState
		want   string
	}{
		{"both at rest", core.DriveNotStarted, core.PhotosNotStarted, "entry"},
		{"drive copying keeps both tracks", core.DriveCopying, core.PhotosAwaitingShare, "tracks"},
		{"drive consenting keeps both tracks", core.DriveConsentPending, core.PhotosTakeoutGuide, "tracks"},
		{"photos guide alone", core.DriveNotStarted, core.PhotosTakeoutGuide, "takeout"},
		{"photos awaiting share alone", core.DriveNotStarted, core.PhotosAwaitingShare, "share"},
		{"photos awaiting upload alone", core.DriveNotStarted, core.PhotosAwaitingUpload, "upload"},
		{"photos importing alone", core.DriveNotStarted, core.PhotosImporting, "waiting"},
		{"photos verifying alone", core.DriveNotStarted, core.PhotosVerifying, "waiting"},
		{"drive done while photos import", core.DriveDone, core.PhotosImporting, "waiting"},
		{"both done", core.DriveDone, core.PhotosDone, "done"},
		{"drive failed wins", core.DriveFailed, core.PhotosImporting, "error"},
		{"photos failed wins", core.DriveCopying, core.PhotosFailed, "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := core.Migration{DriveState: c.drive, PhotosState: c.photos}
			if got := screenFor(m); got != c.want {
				t.Errorf("screenFor(%s, %s) = %q, want %q", c.drive, c.photos, got, c.want)
			}
		})
	}
}

// Every state must map onto a pill modifier the stylesheet actually defines,
// and every state in the core package must be covered.
func TestPillClassCoversEveryState(t *testing.T) {
	driveStates := []string{
		"not_started", "consent_pending", "selecting", "copying", "importing",
		"verifying", "done", "failed", "cancelled",
	}
	photosStates := []string{
		"not_started", "takeout_guide", "awaiting_share", "awaiting_upload",
		"downloading", "importing", "verifying", "done", "failed", "cancelled",
	}
	known := map[string]bool{
		"pill--idle": true, "pill--running": true, "pill--waiting": true,
		"pill--you": true, "pill--done": true, "pill--stopped": true,
	}
	for _, s := range append(driveStates, photosStates...) {
		if c := pillClass(s); !known[c] {
			t.Errorf("pillClass(%q) = %q, which is not a pill modifier in the stylesheet", s, c)
		}
		if l := pillLabel(s); l == "" {
			t.Errorf("pillLabel(%q) is empty", s)
		}
	}
}

// The done screen must not claim a check that is not run. Independent
// verification is a later phase; until it exists, the page may state that the
// copy finished, not that its result was compared against Google.
func TestDoneScreenDoesNotClaimAnUnimplementedCheck(t *testing.T) {
	var b strings.Builder
	err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", page{
		User:   "marco",
		Screen: "done",
		Tracks: []core.TrackSummary{
			{Track: core.TrackDrive, State: "done", Done: true},
			{Track: core.TrackPhotos, State: "done", Done: true},
		},
		DriveFiles:   1200,
		DriveBytes:   4400000000,
		PhotosAssets: 5000,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := b.String()
	for _, claim := range []string{"compared a sample", "byte for byte", "all 500 matched"} {
		if strings.Contains(strings.ToLower(body), claim) {
			t.Errorf("the done screen claims %q, but no verification runs", claim)
		}
	}
}
