// SPDX-License-Identifier: AGPL-3.0-or-later

package nextcloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
)

func TestNewRefusesEmptyURL(t *testing.T) {
	if _, err := New("", 0); err == nil {
		t.Fatal("expected an error for an empty URL")
	}
}

func TestDAVURLUsesTheLoginName(t *testing.T) {
	c, err := New("http://nextcloud/", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.DAVURL("2d4514e3-14d6-47c2-8b38-25cd9de20a44")
	want := "http://nextcloud/remote.php/dav/files/2d4514e3-14d6-47c2-8b38-25cd9de20a44"
	if got != want {
		t.Errorf("DAVURL = %q, want %q", got, want)
	}
}

func TestBeginFlowParsesTheLoginURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/index.php/login/v2" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"poll": {"token": "poll-token", "endpoint": "https://cloud.example/login/v2/poll"},
			"login": "https://cloud.example/login/v2/flow/login-token"
		}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	flow, err := c.BeginFlow(context.Background())
	if err != nil {
		t.Fatalf("BeginFlow: %v", err)
	}
	if flow.PollToken != "poll-token" {
		t.Errorf("PollToken = %q", flow.PollToken)
	}
	if flow.LoginURL != "https://cloud.example/login/v2/flow/login-token" {
		t.Errorf("LoginURL = %q", flow.LoginURL)
	}
}

// A pending flow is a 404, and that is not an error: it just means the person
// has not clicked "Grant access" yet.
func TestPollPendingIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	_, done, err := c.Poll(context.Background(), "poll-token")
	if err != nil {
		t.Fatalf("a pending poll must not be an error, got %v", err)
	}
	if done {
		t.Fatal("done should be false while the flow is pending")
	}
}

func TestPollReturnsCredentialsWhenGranted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("token") != "poll-token" {
			t.Errorf("token = %q", r.Form.Get("token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"server": "https://cloud.example",
			"loginName": "marco-uid",
			"appPassword": "the-app-password"
		}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	creds, done, err := c.Poll(context.Background(), "poll-token")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !done {
		t.Fatal("done should be true once the flow is granted")
	}
	if creds.LoginName != "marco-uid" || creds.AppPassword != "the-app-password" {
		t.Errorf("credentials = %+v", creds)
	}
}

func TestPollIncompleteResultIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server": "https://cloud.example"}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	if _, _, err := c.Poll(context.Background(), "t"); err == nil {
		t.Fatal("an incomplete result must be an error")
	}
}

// The password must never appear when a Credentials value is printed.
func TestCredentialsStringRedactsThePassword(t *testing.T) {
	c := Credentials{LoginName: "marco-uid", AppPassword: "super-secret"}
	if strings.Contains(c.String(), "super-secret") {
		t.Fatalf("String leaked the password: %s", c.String())
	}
	if !strings.Contains(c.String(), "marco-uid") {
		t.Errorf("String should still name the account: %s", c.String())
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	sealer, err := core.NewSealer(core.Secret("test-key"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	want := Credentials{Server: "https://cloud.example", LoginName: "marco-uid", AppPassword: "pw"}

	sealed, err := SealCredentials(sealer, want)
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	if strings.Contains(string(sealed), "pw") {
		t.Fatal("the sealed bytes must not contain the plaintext")
	}
	got, err := OpenCredentials(sealer, sealed)
	if err != nil {
		t.Fatalf("OpenCredentials: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestParseQuotaReadsTheDAVProperties(t *testing.T) {
	body := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">
  <d:response>
    <d:href>/remote.php/dav/files/user/</d:href>
    <d:propstat>
      <d:prop>
        <d:quota-used-bytes>3</d:quota-used-bytes>
        <d:quota-available-bytes>38513916670</d:quota-available-bytes>
      </d:prop>
    </d:propstat>
  </d:response>
</d:multistatus>`

	u, err := parseQuota(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseQuota: %v", err)
	}
	if u.Used != 3 {
		t.Errorf("Used = %d, want 3", u.Used)
	}
	if u.Available != 38513916670 {
		t.Errorf("Available = %d, want 38513916670", u.Available)
	}
	if got := u.Total(); got != 38513916673 {
		t.Errorf("Total = %d, want 38513916673", got)
	}
}

func TestParseQuotaUnlimitedIsNegative(t *testing.T) {
	// Nextcloud reports an unlimited quota as -3 (the internal "unlimited"
	// sentinel). It is a real value, not an error.
	body := `<d:multistatus xmlns:d="DAV:"><d:response><d:propstat><d:prop>
		<d:quota-used-bytes>10</d:quota-used-bytes>
		<d:quota-available-bytes>-3</d:quota-available-bytes>
	</d:prop></d:propstat></d:response></d:multistatus>`

	u, err := parseQuota(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseQuota: %v", err)
	}
	if u.Total() != -1 {
		t.Errorf("Total = %d, want -1 for unlimited", u.Total())
	}
}

func TestParseQuotaWithoutTheProperty(t *testing.T) {
	body := `<d:multistatus xmlns:d="DAV:"><d:response><d:propstat><d:prop>
		<d:getetag>"abc"</d:getetag>
	</d:prop></d:propstat></d:response></d:multistatus>`

	if _, err := parseQuota(strings.NewReader(body)); err != ErrNoQuota {
		t.Errorf("parseQuota error = %v, want ErrNoQuota", err)
	}
}
