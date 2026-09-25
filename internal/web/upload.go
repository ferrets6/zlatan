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

	session, err := opts.Uploads.Begin(user, req.Name, req.Size, req.ChunkSize)
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

// uploadComplete assembles the chunks and queues the import. The state moves
// to importing here, so the wizard's next poll reflects that the work started.
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
		case errors.Is(err, upload.ErrNotFound):
			http.Error(w, "no such upload session", http.StatusNotFound)
		default:
			opts.Log.Error("uploadComplete", "user", user, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	if opts.Runner != nil {
		if err := opts.Runner.StartPhotosUpload(r.Context(), user); err != nil {
			// The archive is safely on disk; the import can be started again.
			opts.Log.Error("uploadComplete: queue import", "user", user, "error", err)
			http.Error(w, "the file was uploaded but the import could not be started", http.StatusConflict)
			return
		}
	}
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
