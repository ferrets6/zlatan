// SPDX-License-Identifier: AGPL-3.0-or-later

package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A missing key falls back to English, which is the kind of gap nobody notices
// in review. This is the test that keeps the two catalogues honest.
func TestEveryLanguageHasEveryKey(t *testing.T) {
	base := catalog[Default]
	for _, l := range Supported {
		if l == Default {
			continue
		}
		for key := range base {
			if _, ok := catalog[l][key]; !ok {
				t.Errorf("%s is missing %q", l, key)
			}
		}
		for key := range catalog[l] {
			if _, ok := base[key]; !ok {
				t.Errorf("%s has %q, which %s does not", l, key, Default)
			}
		}
	}
}

// A phrase with a placeholder is useless if a translation drops it.
func TestPlaceholdersMatchAcrossLanguages(t *testing.T) {
	count := func(s string) int {
		n := 0
		for i := 0; i+1 < len(s); i++ {
			if s[i] == '%' {
				if s[i+1] == '%' {
					i++
					continue
				}
				n++
			}
		}
		return n
	}
	for key, en := range catalog[Default] {
		for _, l := range Supported {
			if got, want := count(catalog[l][key]), count(en); got != want {
				t.Errorf("%s %q has %d placeholders, %s has %d", l, key, got, Default, want)
			}
		}
	}
}

func TestNegotiate(t *testing.T) {
	cases := []struct {
		header string
		want   Lang
	}{
		{"", EN},
		{"it", IT},
		{"it-CH", IT},
		{"IT-ch", IT},
		{"fr-FR,fr;q=0.9", EN},
		{"fr;q=0.9,it;q=0.8", IT},
		{"en;q=0.7,it;q=0.9", IT},
		{"it;q=0.2,en;q=0.9", EN},
		{"it;q=0,en", EN},
		{"it;q=broken,en;q=0.5", EN}, // an unparseable q must not win
		{"de,it,en", IT},             // equal q, browser order decides
	}
	for _, c := range cases {
		if got := Negotiate(c.header); got != c.want {
			t.Errorf("Negotiate(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

// An explicit choice outranks the browser, which is the whole point of the
// switcher: a person reading English on an Italian laptop gets to say so.
func TestCookieBeatsHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "it")
	r.AddCookie(&http.Cookie{Name: Cookie, Value: "en"})
	if got := FromRequest(r); got != EN {
		t.Fatalf("FromRequest = %q, want %q", got, EN)
	}
}

func TestNonsenseCookieFallsBackToTheHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "it")
	r.AddCookie(&http.Cookie{Name: Cookie, Value: "klingon"})
	if got := FromRequest(r); got != IT {
		t.Fatalf("FromRequest = %q, want %q", got, IT)
	}
}

func TestNumbersFollowTheLanguage(t *testing.T) {
	if got, want := Count(EN, 1234567), "1,234,567"; got != want {
		t.Errorf("Count(EN) = %q, want %q", got, want)
	}
	if got, want := Count(IT, 1234567), "1.234.567"; got != want {
		t.Errorf("Count(IT) = %q, want %q", got, want)
	}
	if got, want := Count(EN, -1234), "-1,234"; got != want {
		t.Errorf("Count(EN, negative) = %q, want %q", got, want)
	}
	if got, want := Bytes(IT, 44_181_000_000), "41,1 GiB"; got != want {
		t.Errorf("Bytes(IT) = %q, want %q", got, want)
	}
	if got, want := Bytes(EN, 44_181_000_000), "41.1 GiB"; got != want {
		t.Errorf("Bytes(EN) = %q, want %q", got, want)
	}
}

func TestSpan(t *testing.T) {
	cases := []struct {
		lang Lang
		d    time.Duration
		want string
	}{
		{EN, 30 * time.Second, "30 s"},
		{EN, 10 * time.Minute, "10 min"},
		{IT, 10 * time.Minute, "10 min"},
		{EN, 2*time.Hour + 14*time.Minute, "2 h 14 min"},
		{EN, 3 * time.Hour, "3 h"},
		{EN, 50 * time.Hour, "2 d"},
		{IT, 50 * time.Hour, "2 g"},
	}
	for _, c := range cases {
		if got := Span(c.lang, c.d); got != c.want {
			t.Errorf("Span(%q, %v) = %q, want %q", c.lang, c.d, got, c.want)
		}
	}
}
