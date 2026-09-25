// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"embed"
	"html/template"

	"github.com/marcodellemarche/zlatan/internal/core"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// wizardTemplate renders the wizard. The helper set is deliberately small: the
// template formats a few values and does no logic, because logic belongs in Go
// where it can be tested.
var wizardTemplate = template.Must(
	template.New("wizard.html").Funcs(template.FuncMap{
		"bytes":      core.FormatBytes,
		"trackName":  trackName,
		"stateLabel": stateLabel,
	}).ParseFS(templateFS, "templates/wizard.html"),
)

func trackName(t core.Track) string {
	if t == core.TrackDrive {
		return "Google Drive → Nextcloud"
	}
	return "Google Photos → Immich"
}

// stateLabel turns a state constant into something a person reads.
func stateLabel(state string) string {
	labels := map[string]string{
		"not_started":     "Not started",
		"consent_pending": "Waiting for your Google consent",
		"selecting":       "Choosing what to copy",
		"copying":         "Copying",
		"importing":       "Importing",
		"verifying":       "Checking the result",
		"takeout_guide":   "Preparing the export",
		"awaiting_share":  "Waiting for the shared Takeout folder",
		"awaiting_upload": "Waiting for the Takeout upload",
		"downloading":     "Downloading the Takeout",
		"done":            "Done",
		"failed":          "Something stopped",
		"cancelled":       "Cancelled",
	}
	if label, ok := labels[state]; ok {
		return label
	}
	return state
}
