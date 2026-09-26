// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/i18n"
)

// donePage is the closing screen with both tracks finished, which several
// tests need and none of them should have to spell out.
func donePage() page {
	return page{
		User: "marco", Screen: "done", Lang: i18n.EN,
		Drive:      trackView{Track: core.TrackDrive, State: "done", Done: true},
		Photos:     trackView{Track: core.TrackPhotos, State: "done", Done: true},
		DriveFiles: 1200, DriveBytes: 4400000000, PhotosAssets: 5000,
		NextcloudURL: "https://nextcloud.example.org",
		ImmichURL:    "https://immich.example.org",
	}
}

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
		{"drive copying keeps both tracks", core.DriveCopying, core.PhotosAwaitingTakeout, "tracks"},
		{"drive consenting keeps both tracks", core.DriveConsentPending, core.PhotosTakeoutGuide, "tracks"},
		{"photos guide alone", core.DriveNotStarted, core.PhotosTakeoutGuide, "takeout"},
		{"photos awaiting takeout alone", core.DriveNotStarted, core.PhotosAwaitingTakeout, "waiting"},
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
		"not_started", "takeout_guide", "awaiting_takeout", "awaiting_upload",
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
		// Every state must also have a word, in every language the wizard
		// speaks. T falls back to the key, so an untranslated state shows up
		// here rather than on screen.
		for _, lang := range i18n.Supported {
			key := pillKey(s)
			if word := i18n.T(lang, key); word == key {
				t.Errorf("state %q has no %s word for %q", s, lang, key)
			}
		}
	}
}

// The wizard must not promise work that does not happen. This is the
// repository's "no unimplemented promises" rule: a screen may state what the
// code does and nothing more.
func TestScreensDoNotPromiseUnimplementedWork(t *testing.T) {
	screens := []struct {
		screen string
		p      page
	}{
		{"done-without-checks", donePage()},
		{"waiting", page{
			User: "marco", Screen: "waiting", Lang: i18n.EN,
			Drive:         trackView{Track: core.TrackDrive, State: "done", Done: true},
			Photos:        trackView{Track: core.TrackPhotos, State: "awaiting_takeout"},
			TakeoutFolder: "Takeout", TakeoutPoll: 10 * time.Minute,
			CanUpload: true, CanStartPhotos: true,
		}},
	}
	// Each claim names work that no code performs. Verification now exists, so
	// its sentence is no longer banned; email is still not sent, and the runner
	// still computes no ETA, so those stay.
	banned := []string{
		"we will email",
		"we will send you an email",
		"of 32,900 files",
		"hours left",
	}
	for _, s := range screens {
		t.Run(s.screen, func(t *testing.T) {
			var b strings.Builder
			if err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", s.p); err != nil {
				t.Fatalf("render: %v", err)
			}
			body := strings.ToLower(b.String())
			for _, claim := range banned {
				if strings.Contains(body, claim) {
					t.Errorf("the %s screen claims %q, which no code performs", s.screen, claim)
				}
			}
		})
	}
}

// The done screen states what was actually compared when a verification row
// exists, and falls back to the plain sentence when it does not. It must never
// claim a check that did not run.
func TestDoneScreenStatesTheCheckThatRan(t *testing.T) {
	base := donePage()

	t.Run("no check recorded", func(t *testing.T) {
		var b strings.Builder
		if err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", base); err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(b.String(), "finished without errors") {
			t.Error("with no recorded check the page should fall back to the plain sentence")
		}
	})

	t.Run("a recorded check is shown", func(t *testing.T) {
		p := base
		p.DriveVerification = &core.Verify{Checked: 1200, Matched: 1200}
		var b strings.Builder
		if err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", p); err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(b.String(), "1,200 files against the originals") {
			t.Error("a recorded check should be stated from its own numbers")
		}
	})
}
