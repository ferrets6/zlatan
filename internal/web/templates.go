// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// staticRoot is staticFS rooted at the embedded directory, so the files are
// served at /static/style.css rather than /static/static/style.css. The embed
// directive keeps the directory prefix; StripPrefix in server.go removes it.
var staticRoot = func() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()

// wizardTemplate renders the wizard. The helper set is deliberately small: the
// template formats a few values and does no logic, because logic belongs in Go
// where it can be tested.
var wizardTemplate = template.Must(
	template.New("wizard.html").ParseFS(templateFS, "templates/wizard.html"),
)

// pillClass maps a track state onto the pill modifier the design uses. The
// names are the states in internal/core, so there is no translation table.
func pillClass(state string) string {
	switch state {
	case "not_started":
		return "pill--idle"
	case "done":
		return "pill--done"
	case "failed", "cancelled":
		return "pill--stopped"
	case "takeout_guide", "awaiting_upload":
		return "pill--you"
	case "consent_pending", "awaiting_nextcloud", "awaiting_takeout":
		return "pill--waiting"
	default:
		return "pill--running"
	}
}

// pillKey maps a track state onto the catalogue key for the word in the pill.
// A state that is not named here falls back to the running pill, which is the
// safe answer for anything in flight.
func pillKey(state string) string {
	switch state {
	case "not_started", "selecting":
		return "pill.idle"
	case "consent_pending", "awaiting_nextcloud":
		return "pill.connecting"
	case "copying":
		return "pill.copying"
	case "importing":
		return "pill.importing"
	case "verifying":
		return "pill.checking"
	case "takeout_guide", "awaiting_upload":
		return "pill.waitYou"
	case "awaiting_takeout":
		return "pill.waitGoogle"
	case "downloading":
		return "pill.downloading"
	case "done":
		return "pill.done"
	case "failed", "cancelled":
		return "pill.stopped"
	}
	return "pill.copying"
}

// factsFor is the one line of numbers under a track: what has actually moved.
// It is empty while there is nothing to count, because a row of zeroes tells
// the person less than no row at all.
func factsFor(lang i18n.Lang, v trackView, m core.Migration) string {
	var parts []string
	if v.Track == core.TrackDrive {
		if m.DriveFilesCopied > 0 {
			parts = append(parts, i18n.T(lang, "fact.files", i18n.Count(lang, m.DriveFilesCopied)))
		}
		if m.DriveBytesCopied > 0 {
			parts = append(parts, i18n.Bytes(lang, m.DriveBytesCopied))
		}
	} else if m.PhotosAssetsAdded > 0 {
		parts = append(parts, i18n.T(lang, "fact.photos", i18n.Count(lang, m.PhotosAssetsAdded)))
	}
	return strings.Join(parts, " · ")
}

// screenFor picks which of the design's screens the wizard renders. One page
// is always the truth; this decides which one.
//
// A failure anywhere wins, because the stopped screen shows both tracks and
// says the other one is fine. A finished pair wins next. After that, an active
// Drive track keeps the two-track view: the person asked for files too, and
// hiding that behind a Photos guide would lose it. Only when Drive is not
// running does the Photos half get its own single-track screen.
func screenFor(m core.Migration) string {
	if m.DriveState == core.DriveFailed || m.PhotosState == core.PhotosFailed {
		return "error"
	}
	if m.DriveState == core.DriveDone && m.PhotosState == core.PhotosDone {
		return "done"
	}
	if driveActive(m.DriveState) {
		return "tracks"
	}
	switch m.PhotosState {
	case core.PhotosTakeoutGuide:
		return "takeout"
	case core.PhotosAwaitingUpload:
		return "upload"
	case core.PhotosAwaitingTakeout, core.PhotosDownloading, core.PhotosImporting, core.PhotosVerifying:
		return "waiting"
	}
	if m.DriveState == core.DriveNotStarted && m.PhotosState == core.PhotosNotStarted {
		return "entry"
	}
	return "tracks"
}

// driveActive reports whether the Drive half has work in flight.
func driveActive(s core.DriveState) bool {
	switch s {
	case core.DriveNotStarted, core.DriveDone, core.DriveFailed, core.DriveCancelled:
		return false
	}
	return true
}
