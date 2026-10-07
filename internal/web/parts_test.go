// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
