// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marcodellemarche/zlatan/internal/upload"
)

// uploadBeginRequest is what the browser announces before sending anything.
type uploadBeginRequest struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	ChunkSize int64  `json:"chunkSize"`
	// Hash is the SHA-256 of the whole file, lowercase hex, computed in the
	// browser. It is what makes a resume recognise the same file even when its
	// name changed, and what keeps different content under a name already in
	// use from mixing with it. Optional: a client that omits it still works,
	// without those guarantees.
	Hash string `json:"hash"`
}

// uploadBegin starts or resumes a session. It is idempotent, so a page reload
// does not discard the chunks already uploaded.
func (opts Options) uploadBegin(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Uploads == nil {
		http.Error(w, "uploads are not available on this instance", http.StatusServiceUnavailable)
		return
	}

	var req uploadBeginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "a file name is required", http.StatusBadRequest)
		return
	}

	session, err := opts.Uploads.Begin(user, req.Name, req.Size, req.ChunkSize, req.Hash)
	if err != nil {
		if errors.Is(err, upload.ErrTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		opts.Log.Error("uploadBegin", "user", user, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, opts, session)
}

// uploadChunk stores one chunk. The body is the raw chunk, not multipart: it
// keeps the server simple and the client's fetch() trivial.
func (opts Options) uploadChunk(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Uploads == nil {
		http.Error(w, "uploads are not available on this instance", http.StatusServiceUnavailable)
		return
	}

	name := r.URL.Query().Get("name")
	indexStr := r.URL.Query().Get("index")
	if name == "" || indexStr == "" {
		http.Error(w, "name and index are required", http.StatusBadRequest)
		return
	}
	index, err := strconv.ParseInt(indexStr, 10, 64)
	if err != nil {
		http.Error(w, "index must be a number", http.StatusBadRequest)
		return
	}

	session, err := opts.Uploads.WriteChunk(user, name, index, r.Body)
	if err != nil {
		switch {
		case errors.Is(err, upload.ErrTooLarge):
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		case errors.Is(err, upload.ErrNotFound):
			http.Error(w, "no such upload session", http.StatusNotFound)
		default:
			opts.Log.Error("uploadChunk", "user", user, "index", index, "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, opts, session)
}

// uploadStatus tells the browser what still needs sending, so a resume only
// transfers the missing chunks.
func (opts Options) uploadStatus(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Uploads == nil {
		http.Error(w, "uploads are not available on this instance", http.StatusServiceUnavailable)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	session, err := opts.Uploads.Status(user, name)
	if err != nil {
		if errors.Is(err, upload.ErrNotFound) {
			http.Error(w, "no such upload session", http.StatusNotFound)
			return
		}
		opts.Log.Error("uploadStatus", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, opts, session)
}

// uploadComplete assembles the chunks, and starts the import if this was the
// last part the person declared. A Takeout split into several files must be
// imported in one go: the date and albums of a photo can sit in a JSON file in
// another part, so importing the parts one by one would lose them.
func (opts Options) uploadComplete(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Uploads == nil {
		http.Error(w, "uploads are not available on this instance", http.StatusServiceUnavailable)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if _, err := opts.Uploads.Complete(user, name); err != nil {
		switch {
		case errors.Is(err, upload.ErrIncomplete):
			// 409: the browser is expected to resume and retry, not to give up.
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, upload.ErrMismatch):
			// 422: the chunks were discarded, so the file must be sent again
			// from the start, and the person has to be told that.
			opts.Log.Warn("uploadComplete: the upload does not match its hash", "user", user, "error", err)
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		case errors.Is(err, upload.ErrNotFound):
			http.Error(w, "no such upload session", http.StatusNotFound)
		default:
			opts.Log.Error("uploadComplete", "user", user, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	if opts.Runner != nil {
		// The upload itself is done and on disk; starting the import is a separate
		// step the runner owns. Whether it started the import, moved the track to
		// a reconnect/stopped screen, or will be retried, the page reload shows
		// the outcome — so an error here is logged, never reported as an upload
		// failure (a 409 would make the browser show "Interrupted, try again" and
		// skip the reload, hiding the real state).
		if _, err := opts.Runner.AutoImportIfEnabled(r.Context(), user); err != nil {
			opts.Log.Warn("uploadComplete: auto-import after upload", "user", user, "error", err)
		}
	}
	// The browser reloads after a completed upload and reads the live state from
	// the page, so the response only has to say the upload itself is done.
	writeJSON(w, opts, map[string]any{"complete": true})
}

// uploadAbort discards a session and its chunks.
func (opts Options) uploadAbort(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Uploads == nil {
		http.Error(w, "uploads are not available on this instance", http.StatusServiceUnavailable)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := opts.Uploads.Abort(user, name); err != nil {
		opts.Log.Error("uploadAbort", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
