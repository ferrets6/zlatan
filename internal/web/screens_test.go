// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/i18n"
)

// screens are the states worth looking at, as fixtures. Rendering all of them
// in every language is a smoke test on its own: a template that cannot render
// a state fails here rather than in front of somebody mid-migration.
//
// With ZLATAN_SCREENS_DIR set to an absolute path (see `make screens`) the same
// render is written to disk as the design gallery, so the gallery is the real
// template and cannot drift from it.
func screens(lang i18n.Lang) map[string]page {
	base := page{
		User:           "marco",
		Version:        "0.4.0",
		Lang:           lang,
		Langs:          langChoices(lang),
		NextcloudURL:   "https://nextcloud.example.org",
		NextcloudHost:  "nextcloud.example.org",
		ImmichURL:      "https://immich.example.org",
		ImmichHost:     "immich.example.org",
		TakeoutFolder:  "Takeout",
		TakeoutPoll:    10 * time.Minute,
		CanStartDrive:  true,
		CanStartPhotos: true,
		CanUpload:      true,
	}

	entry := base
	entry.Screen = "entry"
	entry.Drive = trackView{Track: core.TrackDrive, State: "not_started", Pill: i18n.T(lang, "pill.idle"), PillClass: "pill--idle"}
	entry.Photos = trackView{Track: core.TrackPhotos, State: "not_started", Pill: i18n.T(lang, "pill.idle"), PillClass: "pill--idle"}

	tracks := base
	tracks.Screen = "tracks"
	tracks.GoogleConnected, tracks.NextcloudConnected, tracks.CanTakeout, tracks.CanTakeoutRoute = true, true, true, true
	tracks.Drive = trackView{Track: core.TrackDrive, State: "copying", Pill: i18n.T(lang, "pill.copying"), PillClass: "pill--running",
		Progress: "Photos/2019/Sardinia/IMG_2231.jpg"}
	tracks.Photos = trackView{Track: core.TrackPhotos, State: "awaiting_takeout", Pill: i18n.T(lang, "pill.waitGoogle"), PillClass: "pill--waiting"}
	tracks.DriveFiles, tracks.DriveBytes = 12480, 44_181_000_000
	tracks.DriveFacts = i18n.T(lang, "fact.files", i18n.Count(lang, 12480)) + " · " + i18n.Bytes(lang, 44_181_000_000)

	guide := base
	guide.Screen = "takeout"
	guide.GoogleConnected, guide.CanTakeout = true, true
	guide.Drive = trackView{Track: core.TrackDrive, State: "not_started", Pill: i18n.T(lang, "pill.idle"), PillClass: "pill--idle"}
	guide.Photos = trackView{Track: core.TrackPhotos, State: "takeout_guide", Pill: i18n.T(lang, "pill.waitYou"), PillClass: "pill--you"}

	send := guide
	send.Screen = "upload"
	send.Photos.State = "awaiting_upload"

	done := base
	done.Screen = "done"
	done.Drive = trackView{Track: core.TrackDrive, State: "done", Pill: i18n.T(lang, "pill.done"), PillClass: "pill--done", Done: true}
	done.Photos = trackView{Track: core.TrackPhotos, State: "done", Pill: i18n.T(lang, "pill.done"), PillClass: "pill--done", Done: true}
	done.DriveFacts = i18n.T(lang, "fact.files", i18n.Count(lang, 32900)) + " · " + i18n.Bytes(lang, 116_000_000_000)
	done.PhotosFacts = i18n.T(lang, "fact.photos", i18n.Count(lang, 42552))
	done.DriveVerification = &core.Verify{Checked: 500, Matched: 500}

	stopped := base
	stopped.Screen = "error"
	stopped.GoogleConnected, stopped.NextcloudConnected = true, true
	stopped.Drive = trackView{Track: core.TrackDrive, State: "failed", Pill: i18n.T(lang, "pill.stopped"), PillClass: "pill--stopped", Failed: true}
	stopped.Photos = trackView{Track: core.TrackPhotos, State: "importing", Pill: i18n.T(lang, "pill.importing"), PillClass: "pill--running"}
	stopped.DriveFiles = 12480
	stopped.DriveFacts = i18n.T(lang, "fact.files", i18n.Count(lang, 12480))
	stopped.LastError = "rclone copy: exit status 3 — 429 Too Many Requests (userRateLimitExceeded)"

	return map[string]page{
		"01-entry": entry, "02-tracks": tracks, "03-takeout": guide,
		"04-upload": send, "05-done": done, "06-error": stopped,
	}
}

func TestEveryScreenRendersInEveryLanguage(t *testing.T) {
	dir := os.Getenv("ZLATAN_SCREENS_DIR")

	for _, lang := range i18n.Supported {
		for name, p := range screens(lang) {
			var b strings.Builder
			if err := wizardTemplate.ExecuteTemplate(&b, "wizard.html", p); err != nil {
				t.Fatalf("%s in %s: %v", name, lang, err)
			}
			html := b.String()

			// A key that reached the page means a phrase is missing: T returns
			// the key itself rather than blanking the line.
			for _, key := range []string{"btn.", "pill.", "takeout.", "upload.", "wait.", "hint."} {
				if strings.Contains(html, ">"+key) {
					t.Errorf("%s in %s renders a raw catalogue key starting %q", name, lang, key)
				}
			}
			if dir == "" {
				continue
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("make %s: %v", dir, err)
			}
			// The gallery is browsed from disk, where /static/ is not a path.
			out := strings.ReplaceAll(html, `"/static/`, `"../../../internal/web/static/`)
			file := filepath.Join(dir, name+"."+string(lang)+".html")
			if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
				t.Fatalf("write %s: %v", file, err)
			}
		}
	}
}
