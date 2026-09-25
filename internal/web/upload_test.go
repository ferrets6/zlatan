// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/upload"
)

func uploadOptions(t *testing.T, runner Runner) Options {
	t.Helper()
	opts := testOptions(runner)
	opts.Uploads = upload.New(t.TempDir(), 0, 0)
	return opts
}

func beginUpload(t *testing.T, handler http.Handler, user, name string, size, chunk int64) upload.Session {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "size": size, "chunkSize": chunk})
	r := request("POST", "/upload/begin", user)
	r.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var session upload.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return session
}

func putChunk(t *testing.T, handler http.Handler, user, name string, index int64, data string) *httptest.ResponseRecorder {
	t.Helper()
	r := request("PUT", "/upload/chunk?name="+name+"&index="+strconv.FormatInt(index, 10), user)
	r.Body = io.NopCloser(strings.NewReader(data))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

func TestUploadFullFlow(t *testing.T) {
	runner := &fakeRunner{}
	opts := uploadOptions(t, runner)
	handler := Routes(opts)

	// 26 bytes in 10-byte chunks.
	session := beginUpload(t, handler, "marco", "takeout.zip", 26, 10)
	if session.TotalChunks != 3 {
		t.Fatalf("TotalChunks = %d, want 3", session.TotalChunks)
	}

	for i, c := range []string{"abcdefghij", "klmnopqrst", "uvwxyz"} {
		rec := putChunk(t, handler, "marco", "takeout.zip", int64(i), c)
		if rec.Code != http.StatusOK {
			t.Fatalf("chunk %d: code = %d, body = %s", i, rec.Code, rec.Body.String())
		}
	}

	// Complete queues the import.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/upload/complete?name=takeout.zip", "marco"))
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(runner.started) != 1 || runner.started[0] != "upload:marco" {
		t.Fatalf("the import was not queued: %v", runner.started)
	}
}

func TestUploadResumes(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	handler := Routes(opts)

	beginUpload(t, handler, "marco", "takeout.zip", 30, 10)
	// Send only chunks 0 and 2.
	putChunk(t, handler, "marco", "takeout.zip", 0, "0123456789")
	putChunk(t, handler, "marco", "takeout.zip", 2, "0123456789")

	// Ask what is missing.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/upload/status?name=takeout.zip", "marco"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code = %d", rec.Code)
	}
	var session upload.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	missing := session.MissingChunks()
	if len(missing) != 1 || missing[0] != 1 {
		t.Fatalf("missing = %v, want [1]", missing)
	}

	// Completing now must be refused, so the browser resumes instead of
	// producing a truncated archive.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/upload/complete?name=takeout.zip", "marco"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("complete with a hole: code = %d, want 409", rec.Code)
	}
}

// One person must never be able to write into another's upload.
func TestUploadIsScopedToTheCaller(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	handler := Routes(opts)

	beginUpload(t, handler, "federico", "takeout.zip", 10, 10)

	// Marco asks about a name federico is using: he gets his own session, not
	// federico's. A fresh one has nothing received.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/upload/status?name=takeout.zip", "marco"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("marco saw another person's session: code = %d", rec.Code)
	}
}

func TestUploadRefusesWithoutIdentity(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	handler := Routes(opts)

	body, _ := json.Marshal(map[string]any{"name": "a.zip", "size": 10, "chunkSize": 10})
	r := request("POST", "/upload/begin", "")
	r.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestUploadRefusesOversizedFile(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	// A 100-byte cap, then announce 101.
	opts.Uploads = upload.New(t.TempDir(), 0, 100)
	handler := Routes(opts)

	body, _ := json.Marshal(map[string]any{"name": "huge.zip", "size": 101, "chunkSize": 10})
	r := request("POST", "/upload/begin", "marco")
	r.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", rec.Code)
	}
}

func TestUploadRefusesOversizedChunk(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	handler := Routes(opts)

	beginUpload(t, handler, "marco", "a.zip", 20, 10)
	// 11 bytes into a 10-byte chunk.
	rec := putChunk(t, handler, "marco", "a.zip", 0, "0123456789X")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", rec.Code)
	}
}

func TestUploadAbort(t *testing.T) {
	opts := uploadOptions(t, &fakeRunner{})
	handler := Routes(opts)

	beginUpload(t, handler, "marco", "a.zip", 10, 10)
	putChunk(t, handler, "marco", "a.zip", 0, "0123456789")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("DELETE", "/upload?name=a.zip", "marco"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("abort: code = %d, want 204", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/upload/status?name=a.zip", "marco"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after abort: code = %d, want 404", rec.Code)
	}
}

func TestUploadUnavailableWithoutStore(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.Uploads = nil
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/upload/status?name=a.zip", "marco"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}
