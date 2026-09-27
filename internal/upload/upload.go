// SPDX-License-Identifier: AGPL-3.0-or-later

// Package upload stores a large Takeout archive in chunks, so an upload that
// spans hours survives a closed tab, a dropped connection or a restart.
//
// The state lives on disk, not in memory: a 100 GB upload is not something a
// process can afford to lose on restart. A completed archive is a single file
// under the staging root, which is exactly what the runner's immich-go step
// expects to find.
//
// Every chunk is written at its offset in one data file, and the SHA-256 the
// browser announced is computed as the chunks arrive. So completing an upload
// is a rename and a comparison, never a pass over tens of gigabytes inside an
// HTTP request.
package upload

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// Default limits. The chunk is what the browser sends in one request; the file
// cap is a sanity bound, not a policy: a Takeout is split by Google into
// 50 GB parts, so a terabyte is far above anything real.
const (
	DefaultMaxChunk = 64 << 20 // 64 MiB
	DefaultMaxFile  = 1 << 40  // 1 TiB
	manifestName    = "manifest.json"
	dataName        = "data"
	partsSuffix     = ".parts"
	hashSuffix      = ".sha256"

	// layoutVersion is the on-disk shape of a session. A session written by
	// another layout cannot be resumed and is replaced.
	layoutVersion = 2
)

// ErrNotFound means no upload session exists for that name.
var ErrNotFound = errors.New("no upload session")

// ErrIncomplete means not every chunk has arrived yet.
var ErrIncomplete = errors.New("the upload is not complete")

// ErrMismatch means every chunk arrived but together they are not the file
// the browser announced. The chunks are discarded, so the next attempt sends
// the file again instead of reassembling the same wrong bytes.
var ErrMismatch = errors.New("the upload does not match the announced file")

// ErrTooLarge means the declared size or chunk exceeds the configured cap.
var ErrTooLarge = errors.New("the upload exceeds the configured limit")

// Session is what the browser is told about one upload.
type Session struct {
	User      string    `json:"user"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunkSize"`
	CreatedAt time.Time `json:"createdAt"`
	// Hash is the SHA-256 of the whole file, lowercase hex, as the browser
	// computed it. It is the identity of the upload: the same content resumes
	// under any name, and different content never lands in the same file.
	Hash string `json:"hash,omitempty"`

	TotalChunks    int   `json:"totalChunks"`
	ReceivedChunks int   `json:"receivedChunks"`
	Missing        []int `json:"missing,omitempty"`
	Complete       bool  `json:"complete"`
}

// manifest is a session as it is kept on disk.
type manifest struct {
	Version   int       `json:"version"`
	User      string    `json:"user"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunkSize"`
	CreatedAt time.Time `json:"createdAt"`
	Hash      string    `json:"hash,omitempty"`
	Received  []bool    `json:"received"`
	// Absorbed is how many leading chunks the running hash has consumed, and
	// HashState is that hash, serialised. Chunks arrive in order, so the hash
	// keeps up with the upload and is ready when the last chunk lands.
	Absorbed  int    `json:"absorbed"`
	HashState []byte `json:"hashState,omitempty"`
	// HashStale means a chunk the hash had already consumed was rewritten with
	// different bytes, so completing must hash the data file from scratch.
	HashStale bool `json:"hashStale,omitempty"`
}

func (m manifest) total() int { return chunkCount(m.Size, m.ChunkSize) }

// Store holds uploads under a root directory.
type Store struct {
	root     string
	maxChunk int64
	maxFile  int64
	now      func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New builds a store rooted at dir. Limits of zero fall back to the defaults.
func New(root string, maxChunk, maxFile int64) *Store {
	if maxChunk <= 0 {
		maxChunk = DefaultMaxChunk
	}
	if maxFile <= 0 {
		maxFile = DefaultMaxFile
	}
	return &Store{root: root, maxChunk: maxChunk, maxFile: maxFile, now: time.Now, locks: map[string]*sync.Mutex{}}
}

// lock serialises the writers of one session: a chunk retried while its first
// attempt is still being written must not interleave with it.
func (s *Store) lock(dir string) func() {
	s.mu.Lock()
	l, ok := s.locks[dir]
	if !ok {
		l = &sync.Mutex{}
		s.locks[dir] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Begin starts a session, or returns the existing one if the same file is
// already known. It is idempotent so a browser that reloads and re-announces
// the same file does not destroy the chunks already uploaded. The returned
// Name is the one the browser must use from then on, which is not always the
// one it announced:
//
//   - same hash, any name: the content is already here, whole or in part, so
//     a re-downloaded Takeout that Google gave a new suffix resumes instead of
//     starting over.
//   - same name, different content, still uploading: the old chunks can never
//     become this file, so that session is replaced.
//   - same name, different content, already assembled: that archive is kept
//     and this one is stored beside it under a name carrying its hash, so no
//     finished export is ever thrown away.
func (s *Store) Begin(user, name string, size, chunkSize int64, hash string) (Session, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if size <= 0 {
		return Session{}, errors.New("the declared size must be positive")
	}
	if size > s.maxFile {
		return Session{}, fmt.Errorf("%w: %d bytes exceeds the %d byte cap", ErrTooLarge, size, s.maxFile)
	}
	if chunkSize <= 0 || chunkSize > s.maxChunk {
		return Session{}, fmt.Errorf("%w: chunk size must be between 1 and %d", ErrTooLarge, s.maxChunk)
	}

	if hash != "" {
		if found, ok := s.findAssembled(user, size, hash); ok {
			return assembledSession(user, found, size, hash), nil
		}
		if found, ok := s.findSession(user, size, chunkSize, hash); ok {
			return s.Status(user, found)
		}
	}

	if info, err := os.Stat(s.finalPath(user, name)); err == nil {
		if hash == "" || s.assembledHash(user, name) == hash {
			return assembledSession(user, name, info.Size(), hash), nil
		}
		name = hash[:min(12, len(hash))] + "-" + name
	}

	dir, err := s.sessionDir(user, name)
	if err != nil {
		return Session{}, err
	}
	unlock := s.lock(dir)
	defer unlock()

	if existing, err := readManifest(dir); err == nil {
		same := existing.Version == layoutVersion && existing.Size == size && existing.ChunkSize == chunkSize &&
			(hash == "" || existing.Hash == "" || existing.Hash == hash)
		if same {
			// Adopt the hash if the session was begun without one, so it
			// gains an identity without losing its chunks.
			if existing.Hash == "" && hash != "" {
				existing.Hash = hash
				if err := writeManifest(dir, existing); err != nil {
					return Session{}, err
				}
			}
			return s.status(user, existing), nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return Session{}, fmt.Errorf("replace the stale upload: %w", err)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Session{}, err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Session{}, fmt.Errorf("create the upload directory: %w", err)
	}
	m := manifest{
		Version: layoutVersion, User: user, Name: name, Size: size, ChunkSize: chunkSize,
		Hash: hash, CreatedAt: s.now().UTC(), Received: make([]bool, chunkCount(size, chunkSize)),
	}
	if err := writeManifest(dir, m); err != nil {
		return Session{}, err
	}
	return s.status(user, m), nil
}

// WriteChunk stores one chunk. Writing a chunk twice is harmless, which is
// what makes a retry after a dropped connection safe. A body shorter than the
// chunk is what a dropped connection leaves: it is not written, and the chunk
// stays missing so the browser sends it again.
func (s *Store) WriteChunk(user, name string, index int64, r io.Reader) (Session, error) {
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return Session{}, err
	}
	m, err := readManifest(dir)
	if err != nil {
		return Session{}, err
	}
	total := m.total()
	if index < 0 || index >= int64(total) {
		return Session{}, fmt.Errorf("chunk %d is out of range (0..%d)", index, total-1)
	}

	// The body is read before the lock is taken, so a slow connection holds
	// up nobody else. One byte past the chunk size is read, so an oversized
	// body is detected rather than silently truncated.
	body, err := io.ReadAll(io.LimitReader(r, m.ChunkSize+1))
	if err != nil {
		return s.Status(user, name)
	}
	if int64(len(body)) > m.ChunkSize {
		return Session{}, fmt.Errorf("%w: chunk %d is larger than %d bytes", ErrTooLarge, index, m.ChunkSize)
	}
	if int64(len(body)) != expectedChunkSize(m, index) {
		return s.Status(user, name)
	}

	unlock := s.lock(dir)
	defer unlock()
	// Read again under the lock: another request may have moved it on.
	if m, err = readManifest(dir); err != nil {
		return Session{}, err
	}

	f, err := os.OpenFile(filepath.Join(dir, dataName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Session{}, fmt.Errorf("open the upload data: %w", err)
	}
	defer f.Close()
	offset := index * m.ChunkSize

	write := true
	if m.Received[index] {
		existing := make([]byte, len(body))
		if _, err := f.ReadAt(existing, offset); err == nil && bytes.Equal(existing, body) {
			write = false
		} else if int(index) < m.Absorbed {
			m.HashStale = true
		}
	}
	if write {
		if _, err := f.WriteAt(body, offset); err != nil {
			return Session{}, fmt.Errorf("write the chunk: %w", err)
		}
		// The manifest must never claim a chunk the disk does not hold.
		if err := f.Sync(); err != nil {
			return Session{}, fmt.Errorf("write the chunk: %w", err)
		}
		m.Received[index] = true
	}

	if err := absorb(&m, f, index, body); err != nil {
		return Session{}, err
	}
	if err := writeManifest(dir, m); err != nil {
		return Session{}, err
	}
	return s.status(user, m), nil
}

// absorb feeds the running hash every chunk that is now next in line. The one
// just received is already in memory; later ones that arrived early are read
// back from the data file.
func absorb(m *manifest, f *os.File, index int64, body []byte) error {
	if m.Hash == "" || m.HashStale || m.Absorbed >= m.total() || !m.Received[m.Absorbed] {
		return nil
	}
	h, err := restoreHash(m.HashState)
	if err != nil {
		m.HashStale = true
		return nil
	}
	for m.Absorbed < m.total() && m.Received[m.Absorbed] {
		i := int64(m.Absorbed)
		chunk := body
		if i != index {
			chunk = make([]byte, expectedChunkSize(*m, i))
			if _, err := f.ReadAt(chunk, i*m.ChunkSize); err != nil {
				return fmt.Errorf("read back chunk %d: %w", i, err)
			}
		}
		h.Write(chunk)
		m.Absorbed++
	}
	state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	m.HashState = state
	return nil
}

func restoreHash(state []byte) (hash.Hash, error) {
	h := sha256.New()
	if len(state) == 0 {
		return h, nil
	}
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return h, nil
}

// Status reports what is present and what is missing.
func (s *Store) Status(user, name string) (Session, error) {
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return Session{}, err
	}
	m, err := readManifest(dir)
	if err != nil {
		// The parts directory is gone once an upload completes, so a browser
		// that reloads afterwards would otherwise be told the session does not
		// exist. If the assembled archive is there, the upload is done.
		if errors.Is(err, ErrNotFound) {
			if info, statErr := os.Stat(s.finalPath(user, name)); statErr == nil {
				return assembledSession(user, name, info.Size(), s.assembledHash(user, name)), nil
			}
		}
		return Session{}, err
	}
	return s.status(user, m), nil
}

func (s *Store) status(user string, m manifest) Session {
	out := Session{
		User: user, Name: m.Name, Size: m.Size, ChunkSize: m.ChunkSize,
		CreatedAt: m.CreatedAt, Hash: m.Hash, TotalChunks: m.total(),
	}
	for i, ok := range m.Received {
		if ok {
			out.ReceivedChunks++
		} else {
			out.Missing = append(out.Missing, i)
		}
	}
	return out
}

func assembledSession(user, name string, size int64, hash string) Session {
	return Session{User: user, Name: name, Size: size, Hash: hash, Complete: true, TotalChunks: 1, ReceivedChunks: 1}
}

// expectedChunkSize is how large chunk i must be. Every chunk is the full
// chunk size except the last, which holds the remainder; for a file smaller
// than one chunk that remainder is the whole file.
func expectedChunkSize(m manifest, index int64) int64 {
	if index < int64(m.total())-1 {
		return m.ChunkSize
	}
	if rem := m.Size % m.ChunkSize; rem != 0 {
		return rem
	}
	return m.ChunkSize
}

// Complete checks the upload against the announced hash and moves it into
// place. It is idempotent: completing twice returns the same path.
func (s *Store) Complete(user, name string) (string, error) {
	final := s.finalPath(user, name)
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return "", err
	}
	unlock := s.lock(dir)
	defer unlock()

	m, err := readManifest(dir)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if _, statErr := os.Stat(final); statErr == nil {
				return final, nil
			}
		}
		return "", err
	}

	// Refuse to assemble a hole: a missing chunk would produce a truncated
	// archive that looks complete, which is the failure mode this whole
	// package exists to prevent.
	for i, ok := range m.Received {
		if !ok {
			return "", fmt.Errorf("%w: chunk %d is missing", ErrIncomplete, i)
		}
	}
	data := filepath.Join(dir, dataName)
	// A crash between the rename and the cleanup leaves the manifest of an
	// upload that is already in place.
	if _, err := os.Stat(data); os.IsNotExist(err) {
		if _, statErr := os.Stat(final); statErr == nil {
			return final, os.RemoveAll(dir)
		}
	}

	// Prove the bytes are the content the browser announced, so a mismatch is
	// caught here rather than by immich-go after an import that would have to
	// be thrown away.
	if m.Hash != "" {
		sum, err := s.sum(m, data)
		if err != nil {
			return "", fmt.Errorf("hashing the upload: %w", err)
		}
		if sum != m.Hash {
			// Which chunk is wrong cannot be known, so all of them go.
			m.Received = make([]bool, m.total())
			m.Absorbed, m.HashState, m.HashStale = 0, nil, false
			if err := os.Remove(data); err != nil && !os.IsNotExist(err) {
				return "", err
			}
			if err := writeManifest(dir, m); err != nil {
				return "", err
			}
			return "", fmt.Errorf("%w: got %s, announced %s", ErrMismatch, sum, m.Hash)
		}
	}

	if err := os.Truncate(data, m.Size); err != nil {
		return "", fmt.Errorf("size the archive: %w", err)
	}
	if err := os.Rename(data, final); err != nil {
		return "", fmt.Errorf("move the archive into place: %w", err)
	}
	if m.Hash != "" {
		// Kept beside the archive, so a later announcement of the same
		// content is recognised without reading the archive again.
		if err := os.WriteFile(final+hashSuffix, []byte(m.Hash), 0o600); err != nil {
			return final, fmt.Errorf("record the archive's hash: %w", err)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return final, fmt.Errorf("assembled %s but could not remove the parts: %w", final, err)
	}
	return final, nil
}

// sum is the hash of the whole upload: the running one when it kept up, which
// is the normal case, or a pass over the data when a chunk it had already
// consumed was rewritten.
func (s *Store) sum(m manifest, data string) (string, error) {
	if !m.HashStale && m.Absorbed == m.total() {
		h, err := restoreHash(m.HashState)
		if err == nil {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
	}
	return hashFile(data, m.Size)
}

// Abort discards a session and its chunks.
func (s *Store) Abort(user, name string) error {
	dir, err := s.sessionDir(user, name)
	if err != nil {
		return err
	}
	unlock := s.lock(dir)
	defer unlock()
	return os.RemoveAll(dir)
}

// FinalPath returns where a completed archive lives, without checking that it
// exists.
func (s *Store) FinalPath(user, name string) string {
	return s.finalPath(user, name)
}

// findAssembled looks for a finished archive of this content under any name.
func (s *Store) findAssembled(user string, size int64, hash string) (string, bool) {
	sums, _ := filepath.Glob(filepath.Join(s.userDir(user), "*"+hashSuffix))
	for _, sumPath := range sums {
		raw, err := os.ReadFile(sumPath)
		if err != nil || strings.TrimSpace(string(raw)) != hash {
			continue
		}
		archive := strings.TrimSuffix(sumPath, hashSuffix)
		if info, err := os.Stat(archive); err == nil && info.Size() == size {
			return filepath.Base(archive), true
		}
	}
	return "", false
}

// findSession looks for an upload in progress of this content under any name.
func (s *Store) findSession(user string, size, chunkSize int64, hash string) (string, bool) {
	dirs, _ := filepath.Glob(filepath.Join(s.userDir(user), "*"+partsSuffix))
	sort.Strings(dirs)
	for _, dir := range dirs {
		m, err := readManifest(dir)
		if err == nil && m.Version == layoutVersion && m.Hash == hash && m.Size == size && m.ChunkSize == chunkSize {
			return m.Name, true
		}
	}
	return "", false
}

// assembledHash is the recorded hash of a finished archive, or "" when none
// was recorded.
func (s *Store) assembledHash(user, name string) string {
	raw, err := os.ReadFile(s.finalPath(user, name) + hashSuffix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// hashFile returns the lowercase hex SHA-256 of the first size bytes of a
// file, read in a stream so a 50 GB archive never lands in memory.
func hashFile(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readManifest(dir string) (manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return manifest{}, ErrNotFound
		}
		return manifest{}, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, fmt.Errorf("read the manifest: %w", err)
	}
	if len(m.Received) != m.total() {
		// A session from an older layout: nothing in it can be resumed.
		m.Version = 0
		m.Received = make([]bool, m.total())
	}
	return m, nil
}

// writeManifest persists a session's manifest through a rename, so a crash
// mid-write leaves the previous manifest rather than a torn one.
func writeManifest(dir string, m manifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, manifestName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write the manifest: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, manifestName)); err != nil {
		return fmt.Errorf("write the manifest: %w", err)
	}
	return nil
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

// MissingChunks returns the indices still absent, sorted. It is what the
// browser asks for to resume.
func (s Session) MissingChunks() []int {
	out := append([]int(nil), s.Missing...)
	sort.Ints(out)
	return out
}
