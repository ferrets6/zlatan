// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/url"
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
		// The upload panel is the only way to send the files, so awaiting upload
		// gets it whatever Drive is doing; the screen keeps the Drive card.
		// Connecting Google (needed for Photos too) leaves Drive in selecting.
		{"google connected, photos awaiting upload", core.DriveSelecting, core.PhotosAwaitingUpload, "upload"},
		{"drive copying, photos awaiting upload", core.DriveCopying, core.PhotosAwaitingUpload, "upload"},
		{"drive failed, photos awaiting upload", core.DriveFailed, core.PhotosAwaitingUpload, "upload"},
		{"google connected, nothing started", core.DriveSelecting, core.PhotosNotStarted, "tracks"},
		{"drive ready keeps both tracks", core.DriveSelecting, core.PhotosTakeoutGuide, "tracks"},
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
	// The colour must agree with the word: a green "running" dot beside the
	// words "Not started" is a lie the screen tells at a glance, and it is the
	// kind of mismatch that only shows up when a state is looked at closely.
	// Each idle word must be idle, each running word running, and so on.
	toneOfKey := map[string]string{
		"pill.idle": "pill--idle", "pill.connecting": "pill--waiting",
		"pill.copying": "pill--running", "pill.importing": "pill--running",
		"pill.checking": "pill--running", "pill.waitYou": "pill--you",
		"pill.waitGoogle": "pill--waiting", "pill.downloading": "pill--running",
		"pill.done": "pill--done", "pill.stopped": "pill--stopped",
	}
	for _, s := range append(driveStates, photosStates...) {
		if c := pillClass(s); !known[c] {
			t.Errorf("pillClass(%q) = %q, which is not a pill modifier in the stylesheet", s, c)
		}
		if want := toneOfKey[pillKey(s)]; want != "" && pillClass(s) != want {
			t.Errorf("state %q reads %q but is coloured %q, want %q", s, pillKey(s), pillClass(s), want)
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

// A failed import names, on the card, each file it did not bring over, with
// where it is, why, and a search in Immich; and offers to retry only those or
// to accept the result. Only under that reason, read from the Photos progress:
// an older verification's files must not appear beneath a different one, and
// a Drive failure in last_error must not hide them.
func TestFailedImportNamesItsFiles(t *testing.T) {
	v := &core.Verify{Checked: 12, Matched: 11, Mismatch: 1, Problems: []core.Problem{
		{File: "takeout-1-002:Takeout/Foto da 2023/a(1).MP4", Pending: true, Reason: "missing metadata"},
	}}
	m := core.Migration{
		PhotosState:    core.PhotosFailed,
		PhotosProgress: core.EncodeProgress(core.Progress{Key: core.FailImportErrors, Args: []int64{0, 1, 0}}),
	}
	stopped := func(m core.Migration, v *core.Verify) page {
		return page{
			User: "marco", Screen: "error", Lang: i18n.EN,
			ImmichURL:      "https://immich.example.org",
			CanStartPhotos: true, PhotosArchiveReady: true,
			Drive:         trackView{Track: core.TrackDrive, State: "not_started"},
			Photos:        trackView{Track: core.TrackPhotos, State: "failed", Failed: true},
			LastError:     core.EncodeProgress(core.Progress{Key: core.FailImportErrors, Args: []int64{0, 1, 0}}),
			PhotosLeftOut: leftOut(i18n.EN, m, v, "https://immich.example.org"),
		}
	}
	render := func(p page) string {
		var b strings.Builder
		if err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", p); err != nil {
			t.Fatalf("render: %v", err)
		}
		return b.String()
	}

	body := render(stopped(m, v))
	for _, want := range []string{
		`<p class="problem__name">a(1).MP4</p>`,
		"takeout-1-002.zip › Takeout/Foto da 2023",
		"Found, but immich-go never finished it (last step: missing metadata).",
		`href="https://immich.example.org/search?query=`,
		`action="/photos/import/problems"`,
		`action="/photos/import/start"`,
		`action="/photos/accept"`,
		"something was left out: 1 file.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the stopped card should contain %q", want)
		}
	}

	// A list cut at the cap: no "retry only these", which would drop the rest.
	cut := *v
	cut.Mismatch = 500
	body = render(stopped(m, &cut))
	if strings.Contains(body, "/photos/import/problems") || !strings.Contains(body, "/photos/accept") {
		t.Error("a partial list should offer accept and the full retry, not retry only these")
	}

	// The archives purged by the retention: retrying means a new export, and
	// there is nothing to retry one by one.
	purged := stopped(m, v)
	purged.PhotosArchiveReady = false
	purged.CanUpload = true
	body = render(purged)
	if !strings.Contains(body, `action="/photos/takeout/start"`) || !strings.Contains(body, `action="/photos/upload/start"`) ||
		strings.Contains(body, "/photos/import/") {
		t.Error("with the archives gone the card should offer both ways to get them again, not an import of what is not there")
	}

	// No file named (the log would not read): no list and no "retry only
	// these", but a line saying where the names are, and accept.
	body = render(stopped(m, &core.Verify{Checked: 12, Matched: 11, Mismatch: 1}))
	if !strings.Contains(body, "did not name the files") || !strings.Contains(body, "/photos/accept") ||
		strings.Contains(body, "/photos/import/problems") {
		t.Error("with no names the card should point to the log and still offer accept")
	}

	// A name not in "<archive>:<path>" shape is shown as it is, never as ".".
	odd := leftOut(i18n.EN, m, &core.Verify{Mismatch: 1, Problems: []core.Problem{{File: "/staging/marco/.retry-1/0/b.jpg", Pending: true}}}, "")
	if got := odd.Items[0]; got.Name != "b.jpg" || got.Where != "/staging/marco/.retry-1/0" {
		t.Errorf("odd name rendered as %+v", got)
	}
	bare := leftOut(i18n.EN, m, &core.Verify{Mismatch: 1, Problems: []core.Problem{{File: "IMG.MP4"}}}, "")
	if got := bare.Items[0]; got.Name != "IMG.MP4" || got.Where != "" {
		t.Errorf("a bare name rendered as %+v, want no location rather than \".\"", got)
	}

	other := m
	other.PhotosProgress = core.EncodeProgress(core.Progress{Key: core.FailImmichMissing})
	if leftOut(i18n.EN, other, v, "") != nil {
		t.Error("an older run's files must not be listed under a different reason")
	}
}

// The search looks for the photo a file belongs to: the name without its
// extension and without the "(1)" Google gives a second copy, so a left-out
// copy finds the original already in Immich.
func TestImmichSearch(t *testing.T) {
	got := immichSearch("https://immich.example.org/", "20230304_112321(1).MP4")
	want := "https://immich.example.org/search?query=" + url.QueryEscape(`{"originalFileName":"20230304_112321"}`)
	if got != want {
		t.Errorf("immichSearch = %q, want %q", got, want)
	}
	if got := immichSearch("", "a.jpg"); got != "" {
		t.Errorf("with no Immich address there is no link, got %q", got)
	}
}
