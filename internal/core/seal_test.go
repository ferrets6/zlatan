// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealRoundTrip(t *testing.T) {
	s, err := NewSealer(Secret("a-strong-key"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	plaintext := []byte(`{"refresh_token":"1//abc","access_token":"ya29.xyz"}`)
	sealed, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("refresh_token")) {
		t.Fatal("the ciphertext contains the plaintext")
	}

	opened, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip changed the value: %q", opened)
	}
}

// Sealing the same value twice must differ: a repeated nonce would leak that
// two tokens are identical.
func TestSealIsNonDeterministic(t *testing.T) {
	s, _ := NewSealer(Secret("key"))
	first, _ := s.Seal([]byte("same"))
	second, _ := s.Seal([]byte("same"))
	if bytes.Equal(first, second) {
		t.Fatal("sealing the same plaintext twice produced identical bytes")
	}
}

func TestOpenWithWrongKeyFails(t *testing.T) {
	a, _ := NewSealer(Secret("key-a"))
	b, _ := NewSealer(Secret("key-b"))

	sealed, _ := a.Seal([]byte("secret"))
	if _, err := b.Open(sealed); !errors.Is(err, ErrSealed) {
		t.Fatalf("opening with the wrong key should fail with ErrSealed, got %v", err)
	}
}

func TestOpenTamperedFails(t *testing.T) {
	s, _ := NewSealer(Secret("key"))
	sealed, _ := s.Seal([]byte("secret"))

	sealed[len(sealed)-1] ^= 0xff
	if _, err := s.Open(sealed); !errors.Is(err, ErrSealed) {
		t.Fatalf("a tampered value should fail with ErrSealed, got %v", err)
	}
}

func TestOpenTruncatedFails(t *testing.T) {
	s, _ := NewSealer(Secret("key"))
	if _, err := s.Open([]byte("short")); !errors.Is(err, ErrSealed) {
		t.Fatalf("a truncated value should fail with ErrSealed, got %v", err)
	}
}

func TestNewSealerRefusesEmptyKey(t *testing.T) {
	if _, err := NewSealer(Secret("")); err == nil {
		t.Fatal("an empty key must be refused")
	}
}
