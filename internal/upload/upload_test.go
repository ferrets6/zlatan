// SPDX-License-Identifier: AGPL-3.0-or-later

package upload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir(), 0, 0)
}

func begin(t *testing.T, s *Store, name string, size, chunk int64) Session {
	t.Helper()
	session, err := s.Begin("marco", name, size, chunk, "")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return session
}

func TestBeginComputesChunkCount(t *testing.T) {
	s := newStore(t)

	// Exactly divisible: 100 bytes in 10-byte chunks is 10 chunks.
	if got := begin(t, s, "a.zip", 100, 10).TotalChunks; got != 10 {
		t.Errorf("TotalChunks = %d, want 10", got)
	}
	// Not divisible: the last chunk is partial, so 11 chunks.
	if got := begin(t, s, "b.zip", 105, 10).TotalChunks; got != 11 {
		t.Errorf("TotalChunks = %d, want 11", got)
	}
}

func TestBeginIsIdempotent(t *testing.T) {
	s := newStore(t)
	first := begin(t, s, "takeout.zip", 100, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}

	// Re-announcing the same file must not destroy what arrived.
	second, err := s.Begin("marco", "takeout.zip", 100, 10, "")
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if second.ReceivedChunks != 1 {
		t.Errorf("re-announcing the file lost chunks: received = %d", second.ReceivedChunks)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Error("re-announcing the file created a new session")
	}
}

// A different size under the same name is a different file: its chunks
// cannot be resumed, so the session starts over.
func TestBeginReplacesASessionOfAnotherSize(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 100, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}

	session, err := s.Begin("marco", "takeout.zip", 200, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if session.TotalChunks != 20 || session.ReceivedChunks != 0 {
		t.Errorf("session = %+v, want a fresh 20-chunk session", session)
	}
}

func TestWriteChunkAndComplete(t *testing.T) {
	s := newStore(t)
	// 26 bytes in 10-byte chunks: "abcdefghij" "klmnopqrst" "uvwxyz".
	begin(t, s, "takeout.zip", 26, 10)

	chunks := []string{"abcdefghij", "klmnopqrst", "uvwxyz"}
	for i, c := range chunks {
		if _, err := s.WriteChunk("marco", "takeout.zip", int64(i), strings.NewReader(c)); err != nil {
			t.Fatalf("WriteChunk %d: %v", i, err)
		}
	}

	status, err := s.Status("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Complete && status.ReceivedChunks != 3 {
		t.Errorf("received = %d, want 3", status.ReceivedChunks)
	}

	path, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("assembled file = %q", got)
	}
}

func TestStatusReportsMissingChunks(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 30, 10)

	// Write chunks 0 and 2, skip 1.
	for _, i := range []int64{0, 2} {
		if _, err := s.WriteChunk("marco", "takeout.zip", i, strings.NewReader("0123456789")); err != nil {
			t.Fatal(err)
		}
	}

	status, _ := s.Status("marco", "takeout.zip")
	if status.ReceivedChunks != 2 {
		t.Errorf("received = %d, want 2", status.ReceivedChunks)
	}
	missing := status.MissingChunks()
	if len(missing) != 1 || missing[0] != 1 {
		t.Errorf("missing = %v, want [1]", missing)
	}
}

// Completing with a hole must fail: a truncated archive that looks complete is
// the exact failure this package exists to prevent.
func TestCompleteRefusesMissingChunk(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 30, 10)
	for _, i := range []int64{0, 2} {
		if _, err := s.WriteChunk("marco", "takeout.zip", i, strings.NewReader("0123456789")); err != nil {
			t.Fatal(err)
		}
	}

	_, err := s.Complete("marco", "takeout.zip")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
	// The partial final file must not be left behind.
	if _, err := os.Stat(s.FinalPath("marco", "takeout.zip")); err == nil {
		t.Fatal("a failed assembly left a file behind")
	}
}

func TestWriteChunkTwiceOverwrites(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)

	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("first12345")); err != nil {
		t.Fatal(err)
	}
	// A retry after a dropped connection replaces the chunk.
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("second1234")); err != nil {
		t.Fatal(err)
	}

	path, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second1234" {
		t.Fatalf("assembled = %q, want second1234", got)
	}
}

func TestWriteChunkRejectsOutOfRange(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 20, 10)

	if _, err := s.WriteChunk("marco", "takeout.zip", 5, strings.NewReader("x")); err == nil {
		t.Error("an out-of-range chunk index must be refused")
	}
	if _, err := s.WriteChunk("marco", "takeout.zip", -1, strings.NewReader("x")); err == nil {
		t.Error("a negative chunk index must be refused")
	}
}

func TestWriteChunkRejectsOversizedBody(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 20, 10)

	// 11 bytes into a 10-byte chunk.
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789X")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestCompleteIsIdempotent(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}

	first, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if first != second {
		t.Errorf("Complete returned different paths: %q vs %q", first, second)
	}
}

func TestBeginRefusesOversizedFile(t *testing.T) {
	s := New(t.TempDir(), 0, 100)
	_, err := s.Begin("marco", "huge.zip", 101, 10, "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestBeginRefusesOversizedChunk(t *testing.T) {
	s := New(t.TempDir(), 10, 0)
	_, err := s.Begin("marco", "a.zip", 100, 11, "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestBeginRefusesNonPositiveSize(t *testing.T) {
	s := newStore(t)
	if _, err := s.Begin("marco", "a.zip", 0, 10, ""); err == nil {
		t.Error("a zero size must be refused")
	}
}

func TestStatusUnknownSession(t *testing.T) {
	s := newStore(t)
	if _, err := s.Status("marco", "nope.zip"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAbortRemovesEverything(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}

	if err := s.Abort("marco", "takeout.zip"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if _, err := s.Status("marco", "takeout.zip"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Abort, want ErrNotFound, got %v", err)
	}
}

// A hostile name must not escape the staging root.
func TestSanitizeContainment(t *testing.T) {
	s := newStore(t)
	for _, hostile := range []string{
		"../../etc/passwd",
		"..",
		"/etc/passwd",
		"a/../../b",
		"....//....//etc",
	} {
		begin(t, s, hostile, 10, 10)
		if _, err := s.WriteChunk("marco", hostile, 0, strings.NewReader("0123456789")); err != nil {
			t.Fatalf("WriteChunk for %q: %v", hostile, err)
		}
		path, err := s.Complete("marco", hostile)
		if err != nil {
			t.Fatalf("Complete for %q: %v", hostile, err)
		}

		root, _ := filepath.Abs(s.root)
		abs, _ := filepath.Abs(path)
		if !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			t.Errorf("name %q produced a path outside the root: %s", hostile, abs)
		}

		// Distinct hostile names can sanitize to the same element; abort so
		// the next iteration starts clean rather than colliding with this one.
		if err := s.Abort("marco", hostile); err != nil {
			t.Fatalf("Abort for %q: %v", hostile, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %q: %v", path, err)
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"takeout-001.zip", "takeout-001.zip"},
		{"../../etc/passwd", "passwd"},
		{"a b c.zip", "a_b_c.zip"},
		{"..", "upload"},
		{"", "upload"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Two people with the same file name must not collide.
func TestUsersAreIsolated(t *testing.T) {
	s := newStore(t)

	if _, err := s.Begin("marco", "takeout.zip", 10, 10, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin("federico", "takeout.zip", 10, 10, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("marco12345")); err != nil {
		t.Fatal(err)
	}

	// Federico's session must still be empty.
	status, _ := s.Status("federico", "takeout.zip")
	if status.ReceivedChunks != 0 {
		t.Errorf("federico's upload saw marco's chunks: %d", status.ReceivedChunks)
	}
}

// A large, multi-chunk round trip through a reader, to exercise the copy path.
func TestLargeRoundTrip(t *testing.T) {
	s := newStore(t)
	const size = 1 << 20  // 1 MiB
	const chunk = 1 << 16 // 64 KiB

	want := bytes.Repeat([]byte("zlatan!"), size/7+1)[:size]
	begin(t, s, "big.zip", size, chunk)

	for i := int64(0); i*chunk < size; i++ {
		start := i * chunk
		end := start + chunk
		if end > size {
			end = size
		}
		if _, err := s.WriteChunk("marco", "big.zip", i, bytes.NewReader(want[start:end])); err != nil {
			t.Fatalf("WriteChunk %d: %v", i, err)
		}
	}

	path, err := s.Complete("marco", "big.zip")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip corrupted the data: got %d bytes, want %d", len(got), len(want))
	}
}

// After Complete the parts directory is gone. A page that reloads must still
// be told the upload is done, not that the session never existed.
func TestStatusAfterComplete(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Fatal(err)
	}

	status, err := s.Status("marco", "takeout.zip")
	if err != nil {
		t.Fatalf("Status after Complete: %v", err)
	}
	if !status.Complete {
		t.Fatal("a completed upload should report complete")
	}
}

// Re-announcing a completed file must report it complete, not start over.
func TestBeginAfterCompleteReportsComplete(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Fatal(err)
	}

	session, err := s.Begin("marco", "takeout.zip", 10, 10, "")
	if err != nil {
		t.Fatalf("Begin after Complete: %v", err)
	}
	if !session.Complete {
		t.Fatal("re-announcing a completed file should report complete")
	}
}

// A chunk that was truncated by a dropped connection must read as missing, not
// as received: counting it present would let a corrupt archive be assembled.
func TestStatusTreatsATruncatedChunkAsMissing(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 30, 10)

	// Chunk 0 lands whole, chunk 1 is cut short by a dropped connection.
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk("marco", "takeout.zip", 1, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}

	session, err := s.Status("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	if session.ReceivedChunks != 1 {
		t.Errorf("ReceivedChunks = %d, want 1 (the short chunk is not received)", session.ReceivedChunks)
	}
	if len(session.Missing) != 2 || session.Missing[0] != 1 || session.Missing[1] != 2 {
		t.Errorf("Missing = %v, want [1 2]", session.Missing)
	}
}

// Complete must refuse to assemble a truncated chunk rather than produce a
// corrupt archive that looks whole.
func TestCompleteRefusesATruncatedChunk(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 30, 10)
	for i, c := range []string{"0123456789", "abc", "0123456789"} {
		if _, err := s.WriteChunk("marco", "takeout.zip", int64(i), strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Complete("marco", "takeout.zip"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Complete error = %v, want ErrIncomplete", err)
	}
}

// The last chunk is the remainder, not a full chunk: its expected size must be
// the remainder or a correct upload would be refused.
func TestCompleteAcceptsAShortFinalChunk(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 25, 10)
	for i, c := range []string{"0123456789", "0123456789", "01234"} {
		if _, err := s.WriteChunk("marco", "takeout.zip", int64(i), strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	path, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "0123456789012345678901234" {
		t.Errorf("assembled = %q", got)
	}
}

// sha is the lowercase hex SHA-256 of body, as the browser announces it.
func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// upload sends body in chunks under name and completes it.
func upload(t *testing.T, s *Store, name, body string, chunk int64) (Session, string) {
	t.Helper()
	session, err := s.Begin("marco", name, int64(len(body)), chunk, sha(body))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, i := range session.MissingChunks() {
		end := min(int64(len(body)), int64(i+1)*chunk)
		if _, err := s.WriteChunk("marco", session.Name, int64(i), strings.NewReader(body[int64(i)*chunk:end])); err != nil {
			t.Fatalf("WriteChunk %d: %v", i, err)
		}
	}
	path, err := s.Complete("marco", session.Name)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return session, path
}

// A stale upload under the same name can never become the new file, and a
// person must not be stuck until they rename it: the old one is replaced.
func TestBeginReplacesDifferentContentUnderTheSameName(t *testing.T) {
	s := newStore(t)
	if _, err := s.Begin("marco", "takeout.zip", 20, 10, sha("old content, twenty!")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("old conten")); err != nil {
		t.Fatal(err)
	}

	session, err := s.Begin("marco", "takeout.zip", 20, 10, sha("new content, twenty!"))
	if err != nil {
		t.Fatalf("Begin with new content: %v", err)
	}
	if session.ReceivedChunks != 0 || session.Name != "takeout.zip" {
		t.Errorf("session = %+v, want a fresh one under the same name", session)
	}
}

// A finished archive is never thrown away for a new announcement: different
// content under its name is stored beside it.
func TestBeginKeepsAnAssembledArchiveAndStoresNewContentBesideIt(t *testing.T) {
	s := newStore(t)
	_, first := upload(t, s, "takeout.zip", "first archive", 5)

	second, err := s.Begin("marco", "takeout.zip", 14, 5, sha("second archive"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Complete || second.Name == "takeout.zip" {
		t.Fatalf("session = %+v, want a new upload under another name", second)
	}
	if !strings.HasSuffix(second.Name, ".zip") {
		t.Errorf("name %q must stay a .zip, or the import will not find it", second.Name)
	}
	if got, _ := os.ReadFile(first); string(got) != "first archive" {
		t.Errorf("the first archive was changed: %q", got)
	}
}

// Google gives a re-downloaded Takeout a new suffix. The same content under
// the new name must resume the upload already under way.
func TestBeginResumesByContentAcrossNames(t *testing.T) {
	s := newStore(t)
	const body = "0123456789abcdefghij"
	if _, err := s.Begin("marco", "takeout-001.zip", 20, 10, sha(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk("marco", "takeout-001.zip", 0, strings.NewReader(body[:10])); err != nil {
		t.Fatal(err)
	}

	resumed, err := s.Begin("marco", "takeout-001 (1).zip", 20, 10, sha(body))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Name != "takeout-001.zip" || resumed.ReceivedChunks != 1 {
		t.Fatalf("session = %+v, want the existing upload with its chunk", resumed)
	}
}

// And once it is assembled, the same content under any name is done.
func TestBeginRecognisesAnAssembledArchiveAcrossNames(t *testing.T) {
	s := newStore(t)
	upload(t, s, "takeout-001.zip", "the whole archive", 4)

	again, err := s.Begin("marco", "takeout-001 (1).zip", 17, 4, sha("the whole archive"))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Complete || again.Name != "takeout-001.zip" {
		t.Errorf("session = %+v, want the finished archive", again)
	}
}

// A session begun without a hash adopts the one the client later sends, so an
// in-flight upload gains identity without losing its chunks.
func TestBeginAdoptsAHashLater(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 10, 10)
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Begin("marco", "takeout.zip", 10, 10, sha("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Hash != sha("0123456789") || resumed.ReceivedChunks != 1 {
		t.Errorf("session = %+v, want the hash adopted and the chunk kept", resumed)
	}
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Errorf("Complete after adopting the hash: %v", err)
	}
}

// Chunks that arrive out of order, or twice, must still hash to the file.
func TestCompleteHashesChunksReceivedOutOfOrderAndTwice(t *testing.T) {
	s := newStore(t)
	const body = "abcdefghijklmnopqrstuvwxyz"
	if _, err := s.Begin("marco", "takeout.zip", 26, 10, sha(body)); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int64{2, 0, 0, 1, 2} {
		end := min(26, (i+1)*10)
		if _, err := s.WriteChunk("marco", "takeout.zip", i, strings.NewReader(body[i*10:end])); err != nil {
			t.Fatal(err)
		}
	}
	path, err := s.Complete("marco", "takeout.zip")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("archive = %q", got)
	}
}

// A chunk rewritten with different bytes after the running hash consumed it
// must be caught: the hash is recomputed from the data, not trusted.
func TestCompleteRehashesAfterAConsumedChunkChanged(t *testing.T) {
	s := newStore(t)
	const body = "0123456789abcdefghij"
	if _, err := s.Begin("marco", "takeout.zip", 20, 10, sha(body)); err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{"XXXXXXXXXX", "abcdefghij", "0123456789"} {
		index := int64(min(i, 1))
		if i == 2 {
			index = 0
		}
		if _, err := s.WriteChunk("marco", "takeout.zip", index, strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Fatalf("Complete: %v, want the corrected chunk to hash to the file", err)
	}
}

// Many requests for the same session must not interleave their writes. The
// race detector in make check is what makes this test meaningful.
func TestWriteChunkIsSafeForConcurrentUse(t *testing.T) {
	s := newStore(t)
	body := strings.Repeat("0123456789", 20)
	if _, err := s.Begin("marco", "takeout.zip", int64(len(body)), 10, sha(body)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				chunk := body[i*10 : (i+1)*10]
				if _, err := s.WriteChunk("marco", "takeout.zip", int64(i), strings.NewReader(chunk)); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// Complete must verify the bytes against the announced hash, so a mismatch is
// caught here rather than after an import that has to be thrown away. The
// chunks go too: keeping them would reassemble the same wrong bytes forever.
func TestCompleteVerifiesTheAnnouncedHash(t *testing.T) {
	s := newStore(t)
	// SHA-256 of "0123456789" is not this, so the check must fail.
	if _, err := s.Begin("marco", "takeout.zip", 10, 10, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete("marco", "takeout.zip"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("Complete error = %v, want ErrMismatch", err)
	}
	if _, err := os.Stat(s.FinalPath("marco", "takeout.zip")); err == nil {
		t.Error("a mismatched archive must not be left on disk")
	}
	status, err := s.Status("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	if status.ReceivedChunks != 0 {
		t.Errorf("received = %d after a mismatch, want every chunk to be sent again", status.ReceivedChunks)
	}
}

// The correct hash assembles cleanly.
func TestCompleteAcceptsTheMatchingHash(t *testing.T) {
	s := newStore(t)
	const body = "0123456789"
	// SHA-256 of the ten ASCII digits.
	const hash = "84d89877f0d4041efb6bf91a16f0248f2fd573e6af05c19f96bedb9f882f7882"
	if _, err := s.Begin("marco", "takeout.zip", int64(len(body)), 10, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk("marco", "takeout.zip", 0, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete("marco", "takeout.zip"); err != nil {
		t.Fatalf("Complete with the right hash: %v", err)
	}
}

// A session written by the chunk-file layout cannot be resumed by this one.
// It must be replaced, not misread as a session with every chunk present.
func TestBeginReplacesASessionFromTheOldLayout(t *testing.T) {
	s := newStore(t)
	dir, err := s.sessionDir("marco", "takeout.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := `{"user":"marco","name":"takeout.zip","size":20,"chunkSize":10,"hash":"` + sha("0123456789abcdefghij") + `"}`
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00000000.chunk"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := s.Begin("marco", "takeout.zip", 20, 10, sha("0123456789abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	if session.ReceivedChunks != 0 || session.TotalChunks != 2 {
		t.Errorf("session = %+v, want a fresh session", session)
	}
	if _, err := os.Stat(filepath.Join(dir, "00000000.chunk")); err == nil {
		t.Error("the old chunk files should be gone")
	}
}
