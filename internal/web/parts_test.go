// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
)

func postParts(handler http.Handler, value string) *httptest.ResponseRecorder {
	form := url.Values{"parts": {value}}.Encode()
	r := request("POST", "/photos/parts", "marco")
	r.Body = io.NopCloser(strings.NewReader(form))
	r.ContentLength = int64(len(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

func TestDeclarePartsStoresTheCount(t *testing.T) {
	runner := &fakeRunner{}
	handler := Routes(testOptions(runner))

	if rec := postParts(handler, "3"); rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if runner.declared != 3 {
		t.Errorf("declared = %d, want 3", runner.declared)
	}
	if rec := postParts(handler, "three"); rec.Code != http.StatusBadRequest {
		t.Errorf("a non-number: code = %d, want 400", rec.Code)
	}
}

// A finished file that is not the last declared part must not start the
// import: the server says so, and nothing is queued.
func TestUploadCompleteWaitsForTheOtherParts(t *testing.T) {
	runner := &fakeRunner{partsPending: true}
	handler := Routes(uploadOptions(t, runner))

	beginUpload(t, handler, "marco", "takeout-001.zip", 4, 10)
	putChunk(t, handler, "marco", "takeout-001.zip", 0, "abcd")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/upload/complete?name=takeout-001.zip", "marco"))
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(runner.started) != 0 {
		t.Errorf("one part of several started the import: %v", runner.started)
	}
}

// The upload screen asks for the count first, and offers the file picker only
// once it is known.
func TestUploadScreenAsksForTheCountFirst(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.State.(*fakeState).migrations["marco"] = core.Migration{
		User: "marco", DriveState: core.DriveNotStarted, PhotosState: core.PhotosAwaitingUpload,
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()
	if !strings.Contains(body, `action="/photos/parts"`) {
		t.Fatal("the count form is missing")
	}
	if strings.Contains(body, `id="upload-input"`) {
		t.Error("the file picker was offered before the count")
	}

	opts.Runner = &fakeRunner{parts: core.Parts{Expected: 3, Have: 1, Missing: []int{2, 3}}}
	handler = Routes(opts)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body = rec.Body.String()
	if !strings.Contains(body, `id="upload-input"`) || !strings.Contains(body, "2, 3") {
		t.Errorf("with a count, the picker and the missing parts should show:\n%s", body)
	}
}

// On the upload screen /status carries the parts count, so the page updates it
// live as parts arrive (a kiosk download) without a manual reload.
func TestStatusCarriesPartsWhileAwaitingUpload(t *testing.T) {
	opts := testOptions(&fakeRunner{parts: core.Parts{Expected: 3, Have: 1, Missing: []int{2, 3}}})
	opts.State.(*fakeState).migrations["marco"] = core.Migration{
		User: "marco", DriveState: core.DriveNotStarted, PhotosState: core.PhotosAwaitingUpload,
		PhotosPartsExpected: 3,
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/status", "marco"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"parts"`) || !strings.Contains(body, `"complete":false`) {
		t.Errorf("status should carry the parts count while awaiting upload: %s", body)
	}

	// Count not declared yet: there is no "X of Y" line to update.
	opts.State.(*fakeState).migrations["marco"] = core.Migration{
		User: "marco", DriveState: core.DriveNotStarted, PhotosState: core.PhotosAwaitingUpload,
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/status", "marco"))
	if strings.Contains(rec.Body.String(), `"parts"`) {
		t.Errorf("parts should wait for the declared count: %s", rec.Body.String())
	}

	// Not on the upload screen: no parts object.
	opts.State.(*fakeState).migrations["marco"] = core.Migration{
		User: "marco", DriveState: core.DriveCopying, PhotosState: core.PhotosNotStarted,
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/status", "marco"))
	if strings.Contains(rec.Body.String(), `"parts"`) {
		t.Errorf("parts should only be carried while awaiting upload: %s", rec.Body.String())
	}
}

// Someone moving both halves: Google and Nextcloud connected, so the Drive copy
// is ready (selecting), and the photos are to be sent by hand. The upload screen
// must offer the parts form and still let them start the Drive copy.
func TestUploadScreenKeepsDriveStartable(t *testing.T) {
	store := newFakeTokenStore()
	store.tokens["marco/google"] = core.Token{User: "marco", Provider: "google", Sealed: []byte("x")}
	store.tokens["marco/nextcloud"] = core.Token{User: "marco", Provider: "nextcloud", Sealed: []byte("x")}
	store.tokens["marco/immich"] = core.Token{User: "marco", Provider: "immich", Sealed: []byte("x")}
	opts := oauthOptions(&fakeGoogle{}, store)
	opts.Runner = &fakeRunner{}
	opts.State.(*fakeState).migrations["marco"] = core.Migration{
		User: "marco", DriveState: core.DriveSelecting, PhotosState: core.PhotosAwaitingUpload,
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()
	if !strings.Contains(body, `action="/photos/parts"`) {
		t.Error("the upload panel is missing")
	}
	if !strings.Contains(body, `data-track="drive"`) || !strings.Contains(body, `action="/drive/start"`) {
		t.Error("the Drive copy must stay startable from the upload screen")
	}
}

// Both ways out of an import that left files out reach the runner and go back
// to the wizard, even when the runner refuses (a page left open, clicked
// twice): the page then shows where the track really is.
func TestSettlePhotosRoutes(t *testing.T) {
	for path, want := range map[string]string{
		"/photos/import/problems": "problems:marco",
		"/photos/accept":          "accept:marco",
	} {
		runner := &fakeRunner{}
		handler := Routes(testOptions(runner))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request("POST", path, "marco"))
		if rec.Code != http.StatusSeeOther || !slices.Contains(runner.started, want) {
			t.Errorf("POST %s = %d, runner saw %v; want 303 and %q", path, rec.Code, runner.started, want)
		}

		runner = &fakeRunner{err: core.ErrNoProblemsToSettle}
		handler = Routes(testOptions(runner))
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, request("POST", path, "marco"))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("POST %s refused by the runner = %d, want 303 back to the wizard", path, rec.Code)
		}
	}
}
