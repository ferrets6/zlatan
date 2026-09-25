// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// Sealer encrypts and decrypts OAuth tokens at rest. Zlatan holds the Google
// refresh tokens of every person who has used it, and each one grants read
// access to that person's entire Drive: the stored form must be useless
// without the key, which lives in the environment and never in the database.
type Sealer struct {
	aead cipher.AEAD
}

// ErrSealed reports a value that cannot be opened: the key is wrong, or the
// ciphertext was tampered with. Both are indistinguishable by design.
var ErrSealed = errors.New("sealed value cannot be opened")

// NewSealer derives a 256-bit key from the configured secret. The secret is
// hashed rather than used directly, so any length is acceptable.
func NewSealer(key Secret) (*Sealer, error) {
	if key.Empty() {
		return nil, errors.New("the sealing key is empty")
	}
	sum := sha256.Sum256([]byte(key.Reveal()))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("building the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("building GCM: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext. The nonce is random and prepended to the
// ciphertext, so sealing the same value twice yields different bytes.
func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating a nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, ErrSealed
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrSealed
	}
	return plaintext, nil
}
