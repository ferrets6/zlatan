// SPDX-License-Identifier: AGPL-3.0-or-later

// Package i18n holds the wizard's text in every language it speaks, and picks
// one per request.
//
// The order is: an explicit choice (cookie), then the browser's
// Accept-Language, then English. Geolocation is not used: the browser already
// states what its owner reads, and on a home network every request comes from
// the same address anyway.
package i18n

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// Lang is a language the wizard speaks.
type Lang string

const (
	EN Lang = "en"
	IT Lang = "it"
)

// Default is used when nothing else matches.
const Default = EN

// Supported is every language, in the order the switcher shows them.
var Supported = []Lang{EN, IT}

// Cookie is where an explicit choice is kept.
const Cookie = "zlatan_lang"

// Param is the query parameter that sets the choice.
const Param = "lang"

// Parse returns the language for a tag such as "it" or "it-CH".
func Parse(tag string) (Lang, bool) {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	for _, l := range Supported {
		if string(l) == base {
			return l, true
		}
	}
	return Default, false
}

// Negotiate picks the best match from an Accept-Language header. Unknown tags
// are skipped rather than failing the whole header, and a malformed q value is
// treated as q=0 so a broken entry cannot outrank a good one.
func Negotiate(header string) Lang {
	type option struct {
		lang Lang
		q    float64
		pos  int
	}
	var options []option

	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		lang, ok := Parse(tag)
		if !ok {
			continue
		}
		q := 1.0
		if _, raw, found := strings.Cut(params, "q="); found {
			q, _ = strconv.ParseFloat(strings.TrimSpace(raw), 64)
		}
		if q <= 0 {
			continue
		}
		options = append(options, option{lang, q, i})
	}
	if len(options) == 0 {
		return Default
	}
	// Highest q wins; ties go to whichever the browser listed first.
	sort.SliceStable(options, func(a, b int) bool {
		if options[a].q != options[b].q {
			return options[a].q > options[b].q
		}
		return options[a].pos < options[b].pos
	})
	return options[0].lang
}

// FromRequest resolves the language for one request.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(Cookie); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	return Negotiate(r.Header.Get("Accept-Language"))
}

// SetCookie remembers an explicit choice. It is not marked Secure because the
// wizard is also reachable over plain HTTP on the home network, and the value
// is a two-letter language tag.
func SetCookie(w http.ResponseWriter, l Lang) {
	http.SetCookie(w, &http.Cookie{
		Name:     Cookie,
		Value:    string(l),
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// T returns the phrase for a key, filled in with args. An unknown key falls
// back to English and then to the key itself, so a gap shows up on screen
// instead of rendering as a blank.
func T(l Lang, key string, args ...any) string {
	phrase, ok := catalog[l][key]
	if !ok {
		if phrase, ok = catalog[Default][key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return phrase
	}
	return fmt.Sprintf(phrase, args...)
}

// Name is the language's own name, for the switcher.
func Name(l Lang) string { return T(l, "lang.name") }

// Count groups thousands the way the language does.
func Count(l Lang, n int64) string {
	sep := ","
	if l == IT {
		sep = "."
	}
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Bytes renders a size in the units a person thinks in, with the decimal mark
// the language uses.
func Bytes(l Lang, n int64) string {
	s := core.FormatBytes(n)
	if l == IT {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// Span renders a duration in compact units, which keeps it clear of plural
// rules in every language.
func Span(l Lang, d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d %s", int(d.Seconds()), T(l, "time.sec"))
	case d < time.Hour:
		return fmt.Sprintf("%d %s", int(d.Minutes()), T(l, "time.min"))
	case d < 24*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%d %s %d %s", h, T(l, "time.hour"), m, T(l, "time.min"))
		}
		return fmt.Sprintf("%d %s", h, T(l, "time.hour"))
	default:
		return fmt.Sprintf("%d %s", int(d.Hours())/24, T(l, "time.day"))
	}
}
