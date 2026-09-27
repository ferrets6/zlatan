// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"

	"github.com/marcodellemarche/zlatan/internal/config"

	"github.com/marcodellemarche/zlatan/internal/i18n"
	"github.com/marcodellemarche/zlatan/internal/oauth"
)

// legalUpdated is the date the privacy policy and the terms last changed.
// Update it whenever their wording does: a policy without a date is not one.
const legalUpdated = "27 September 2026"

// legalPage is the public face of the service: the landing page, the privacy
// policy and the terms. None of it needs an identity, because Google's OAuth
// verification requires all three to be readable without signing in, and a
// home page that is only a login screen fails review.
type legalPage struct {
	Lang    i18n.Lang
	Langs   []langChoice
	Version string

	// Doc is "privacy" or "terms". The landing page renders its own template.
	Doc string

	PublicURL     string
	ContactEmail  string
	NextcloudHost string
	ImmichHost    string
	RetentionDays int
	Scope         string
	Updated       string
}

func (p legalPage) T(key string, args ...any) string { return i18n.T(p.Lang, key, args...) }

// chooseLang applies an explicit language choice and reports whether the
// request was answered with a redirect. Shared by every public page so the
// switcher behaves the same everywhere, with no JavaScript.
func chooseLang(w http.ResponseWriter, r *http.Request) (i18n.Lang, bool) {
	if choice := r.URL.Query().Get(i18n.Param); choice != "" {
		if l, ok := i18n.Parse(choice); ok {
			i18n.SetCookie(w, l)
		}
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return i18n.Default, true
	}
	return i18n.FromRequest(r), false
}

func (opts Options) legalPageFor(doc string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang, done := chooseLang(w, r)
		if done {
			return
		}
		opts.renderPublic(w, "legal.html", doc, lang)
	}
}

// landing is what an unauthenticated visitor sees at the root. It describes
// what the service does and links to the policy, which is what Google asks a
// home page to do, and it tells a household member how to get in.
func (opts Options) landing(w http.ResponseWriter, r *http.Request) {
	lang, done := chooseLang(w, r)
	if done {
		return
	}
	opts.renderPublic(w, "landing.html", "", lang)
}

func (opts Options) renderPublic(w http.ResponseWriter, name, doc string, lang i18n.Lang) {
	// An unset retention means the runner's default, so the page states the
	// number of days that will actually apply rather than zero.
	retention := int(opts.Config.StagingRetention.Hours() / 24)
	if retention <= 0 {
		retention = config.DefaultRetentionDays
	}
	p := legalPage{
		Lang:          lang,
		Langs:         langChoices(lang),
		Version:       opts.Version,
		Doc:           doc,
		PublicURL:     opts.Config.PublicURL,
		ContactEmail:  opts.Config.ContactEmail,
		NextcloudHost: hostOf(opts.Config.Nextcloud.WebURL()),
		ImmichHost:    hostOf(opts.Config.Immich.WebURL()),
		RetentionDays: retention,
		Scope:         oauth.DriveScope,
		Updated:       legalUpdated,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	if err := publicTemplate.ExecuteTemplate(w, name, p); err != nil {
		opts.Log.Error("public page: render", "page", name, "error", err)
	}
}
