// SPDX-License-Identifier: AGPL-3.0-or-later

package upload

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir(), 0, 0)
}

func begin(t *testing.T, s *Store, name string, size, chunk int64) Session {
	t.Helper()
	session, err := s.Begin("marco", name, size, chunk)
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
	second, err := s.Begin("marco", "takeout.zip", 100, 10)
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

func TestBeginRefusesSizeChange(t *testing.T) {
	s := newStore(t)
	begin(t, s, "takeout.zip", 100, 10)

	_, err := s.Begin("marco", "takeout.zip", 200, 10)
	if err == nil {
		t.Fatal("announcing a different size under the same name must be refused")
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
	_, err := s.Begin("marco", "huge.zip", 101, 10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestBeginRefusesOversizedChunk(t *testing.T) {
	s := New(t.TempDir(), 10, 0)
	_, err := s.Begin("marco", "a.zip", 100, 11)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestBeginRefusesNonPositiveSize(t *testing.T) {
	s := newStore(t)
	if _, err := s.Begin("marco", "a.zip", 0, 10); err == nil {
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

	if _, err := s.Begin("marco", "takeout.zip", 10, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin("federico", "takeout.zip", 10, 10); err != nil {
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

	session, err := s.Begin("marco", "takeout.zip", 10, 10)
	if err != nil {
		t.Fatalf("Begin after Complete: %v", err)
	}
	if !session.Complete {
		t.Fatal("re-announcing a completed file should report complete")
	}
}
