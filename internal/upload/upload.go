// SPDX-License-Identifier: AGPL-3.0-or-later

// Package upload stores a large Takeout archive in chunks, so an upload that
// spans hours survives a closed tab, a dropped connection or a restart.
//
// The state lives on disk, not in memory: a 100 GB upload is not something a
// process can afford to lose on restart. A completed archive is a single file
// under the staging root, which is exactly what the runner's immich-go step
// expects to find.
package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// Default limits. The chunk is what the browser sends in one request; the file
// cap is a sanity bound, not a policy — a Takeout is split by Google into
// 50 GB parts, so a terabyte is far above anything real.
const (
	DefaultMaxChunk = 64 << 20 // 64 MiB
	DefaultMaxFile  = 1 << 40  // 1 TiB
	manifestName    = "manifest.json"
	partsSuffix     = ".parts"
)

// ErrNotFound means no upload session exists for that name.
var ErrNotFound = errors.New("no upload session")

// ErrIncomplete means not every chunk has arrived yet.
var ErrIncomplete = errors.New("the upload is not complete")

// ErrTooLarge means the declared size or chunk exceeds the configured cap.
var ErrTooLarge = errors.New("the upload exceeds the configured limit")

// Session is the state of one upload.
type Session struct {
	User      string    `json:"user"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunkSize"`
	CreatedAt time.Time `json:"createdAt"`

	// derived, not persisted
	TotalChunks    int   `json:"totalChunks"`
	ReceivedChunks int   `json:"receivedChunks"`
	Missing        []int `json:"missing,omitempty"`
	Complete       bool  `json:"complete"`
}

// Store holds uploads under a root directory.
type Store struct {
	root     string
	maxChunk int64
	maxFile  int64
	now      func() time.Time
}

// New builds a store rooted at dir. Limits of zero fall back to the defaults.
func New(root string, maxChunk, maxFile int64) *Store {
	if maxChunk <= 0 {
		maxChunk = DefaultMaxChunk
	}
	if maxFile <= 0 {
		maxFile = DefaultMaxFile
	}
	return &Store{root: root, maxChunk: maxChunk, maxFile: maxFile, now: time.Now}
}

// Begin starts a session, or returns the existing one if the name is already
// known. It is idempotent so a browser that reloads and re-announces the same
// file does not destroy the chunks already uploaded.
func (s *Store) Begin(user, name string, size, chunkSize int64) (Session, error) {
	if size <= 0 {
		return Session{}, errors.New("the declared size must be positive")
	}
	if size > s.maxFile {
		return Session{}, fmt.Errorf("%w: %d bytes exceeds the %d byte cap", ErrTooLarge, size, s.maxFile)
	}
	if chunkSize <= 0 || chunkSize > s.maxChunk {
		return Session{}, fmt.Errorf("%w: chunk size must be between 1 and %d", ErrTooLarge, s.maxChunk)
	}

	// Already assembled: report it complete rather than reopening a session,
	// which would let a reloaded page re-upload a file the server already has.
	if info, err := os.Stat(s.finalPath(user, name)); err == nil {
		return Session{
			User: user, Name: name, Size: info.Size(),
			Complete: true, TotalChunks: 1, ReceivedChunks: 1,
		}, nil
	}

	dir, err := s.sessionDir(user, name)
	if err != nil {
		return Session{}, err
	}
	if existing, err := s.readManifest(dir); err == nil {
		// Same name, same size: resume. Different size: the caller is
		// announcing a different file under a name already in use, which is
		// an error rather than a silent overwrite.
		if existing.Size != size {
			return Session{}, fmt.Errorf("an upload named %q already exists with a different size", name)
		}
		return s.Status(user, name)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Session{}, fmt.Errorf("create the upload directory: %w", err)
	}
	manifest := Session{User: user, Name: name, Size: size, ChunkSize: chunkSize, CreatedAt: s.now().UTC()}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return Session{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), raw, 0o600); err != nil {
		return Session{}, fmt.Errorf("write the manifest: %w", err)
	}
	return s.Status(user, name)
}

// WriteChunk stores one chunk. Writing a chunk twice is harmless: it is
// overwritten, which is what makes a retry after a dropped connection safe.
func (s *Store) WriteChunk(user, name string, index int64, r io.Reader) (Session, error) {
	manifest, dir, err := s.load(user, name)
	if err != nil {
		return Session{}, err
	}

	total := chunkCount(manifest.Size, manifest.ChunkSize)
	if index < 0 || index >= int64(total) {
		return Session{}, fmt.Errorf("chunk %d is out of range (0..%d)", index, total-1)
	}

	// Read one byte past the chunk size, so an oversized body is detected
	// rather than silently truncated.
	path := filepath.Join(dir, chunkFile(index))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Session{}, fmt.Errorf("open the chunk file: %w", err)
	}
	written, err := io.Copy(f, io.LimitReader(r, manifest.ChunkSize+1))
	closeErr := f.Close()
	if err != nil {
		return Session{}, fmt.Errorf("write the chunk: %w", err)
	}
	if closeErr != nil {
		return Session{}, fmt.Errorf("close the chunk: %w", closeErr)
	}
	if written > manifest.ChunkSize {
		os.Remove(path)
		return Session{}, fmt.Errorf("%w: chunk %d is larger than %d bytes", ErrTooLarge, index, manifest.ChunkSize)
	}

	return s.Status(user, name)
}

// Status reports what is present and what is missing.
func (s *Store) Status(user, name string) (Session, error) {
	manifest, dir, err := s.load(user, name)
	if err != nil {
		// The parts directory is gone once an upload completes, so a browser
		// that reloads afterwards would otherwise be told the session does not
		// exist. If the assembled archive is there, the upload is done.
		if errors.Is(err, ErrNotFound) {
			if info, statErr := os.Stat(s.finalPath(user, name)); statErr == nil {
				return Session{
					User: user, Name: name, Size: info.Size(),
					Complete: true, TotalChunks: 1, ReceivedChunks: 1,
				}, nil
			}
		}
		return Session{}, err
	}
	total := chunkCount(manifest.Size, manifest.ChunkSize)
	manifest.TotalChunks = total

	for i := 0; i < total; i++ {
		if _, err := os.Stat(filepath.Join(dir, chunkFile(int64(i)))); err == nil {
			manifest.ReceivedChunks++
		} else {
			manifest.Missing = append(manifest.Missing, i)
		}
	}

	// A completed upload has the final file and no parts directory.
	if _, err := os.Stat(s.finalPath(user, name)); err == nil {
		manifest.Complete = true
		manifest.ReceivedChunks = total
		manifest.Missing = nil
	}
	return manifest, nil
}

// Complete assembles the chunks into the final file and removes the parts. It
// is idempotent: completing twice returns the same path.
func (s *Store) Complete(user, name string) (string, error) {
	final := s.finalPath(user, name)
	if _, err := os.Stat(final); err == nil {
		return final, nil
	}

	manifest, dir, err := s.load(user, name)
	if err != nil {
		return "", err
	}
	total := chunkCount(manifest.Size, manifest.ChunkSize)

	// Refuse to assemble a hole: a missing chunk would produce a truncated
	// archive that looks complete, which is the failure mode this whole
	// package exists to prevent.
	for i := 0; i < total; i++ {
		info, err := os.Stat(filepath.Join(dir, chunkFile(int64(i))))
		if err != nil {
			return "", fmt.Errorf("%w: chunk %d is missing", ErrIncomplete, i)
		}
		if info.Size() == 0 && manifest.Size > 0 && i < total-1 {
			return "", fmt.Errorf("%w: chunk %d is empty", ErrIncomplete, i)
		}
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return "", err
	}
	out, err := os.OpenFile(final, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("create the archive: %w", err)
	}
	for i := 0; i < total; i++ {
		chunk, err := os.Open(filepath.Join(dir, chunkFile(int64(i))))
		if err != nil {
			out.Close()
			os.Remove(final)
			return "", fmt.Errorf("open chunk %d: %w", i, err)
		}
		if _, err := io.Copy(out, chunk); err != nil {
			chunk.Close()
			out.Close()
			os.Remove(final)
			return "", fmt.Errorf("append chunk %d: %w", i, err)
		}
		chunk.Close()
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}

	// Only now is the parts directory redundant.
	if err := os.RemoveAll(dir); err != nil {
		return final, fmt.Errorf("assembled %s but could not remove the parts: %w", final, err)
	}
	return final, nil
}

// Abort discards a session and its chunks.
func (s *Store) Abort(user, name string) error {
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// FinalPath returns where a completed archive lives, without checking that it
// exists.
func (s *Store) FinalPath(user, name string) string {
	return s.finalPath(user, name)
}

func (s *Store) load(user, name string) (Session, string, error) {
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return Session{}, "", err
	}
	manifest, err := s.readManifest(dir)
	if err != nil {
		return Session{}, "", err
	}
	return manifest, dir, nil
}

func (s *Store) readManifest(dir string) (Session, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	var manifest Session
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Session{}, fmt.Errorf("read the manifest: %w", err)
	}
	return manifest, nil
}

// userDir is where one person's completed archives live. It uses the shared
// sanitizer, so the runner reads from exactly the directory this package
// writes to.
func (s *Store) userDir(user string) string {
	return filepath.Join(s.root, core.SafeName(user))
}

// sessionDir is the parts directory for one upload. Both the user and the name
// come from outside, so both are sanitized and the result is checked to be
// under the root.
func (s *Store) sessionDir(user, name string) (string, error) {
	dir := filepath.Join(s.userDir(user), sanitize(name)+partsSuffix)
	if err := s.withinRoot(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// finalPath is where a completed archive lives.
func (s *Store) finalPath(user, name string) string {
	return filepath.Join(s.userDir(user), sanitize(name))
}

// withinRoot refuses a path that escaped the root, which sanitize should
// already have made impossible: this is the belt to its braces.
func (s *Store) withinRoot(path string) error {
	root, err := filepath.Abs(s.root)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return fmt.Errorf("refusing a path outside the staging root: %s", path)
	}
	return nil
}

// sanitize turns a name into a safe path element. Only letters, digits, dash,
// dot and underscore survive; everything else, including path separators and
// the dots that make up "..", becomes an underscore.
func sanitize(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "upload"
	}
	return out
}

func chunkCount(size, chunkSize int64) int {
	if chunkSize <= 0 {
		return 0
	}
	n := int((size + chunkSize - 1) / chunkSize)
	if n == 0 {
		n = 1
	}
	return n
}

func chunkFile(index int64) string {
	return fmt.Sprintf("%08d.chunk", index)
}

// MissingChunks returns the indices still absent, sorted. It is what the
// browser asks for to resume.
func (s Session) MissingChunks() []int {
	out := append([]int(nil), s.Missing...)
	sort.Ints(out)
	return out
}
