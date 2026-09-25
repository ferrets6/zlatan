// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"embed"
	"html/template"

	"github.com/marcodellemarche/migrate/internal/core"
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
		"not_started":     "Non iniziato",
		"consent_pending": "In attesa del consenso Google",
		"selecting":       "Selezione dei contenuti",
		"copying":         "Copia in corso",
		"importing":       "Importazione in corso",
		"verifying":       "Verifica in corso",
		"takeout_guide":   "Preparazione dell'esportazione",
		"awaiting_share":  "In attesa della condivisione del Takeout",
		"awaiting_upload": "In attesa del caricamento del Takeout",
		"downloading":     "Download del Takeout",
		"done":            "Completato",
		"failed":          "Errore",
		"cancelled":       "Annullato",
	}
	if label, ok := labels[state]; ok {
		return label
	}
	return state
}
