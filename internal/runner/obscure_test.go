// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"testing"
)

// reveal is the inverse of obscurePassword, implemented independently here so
// the test does not just call the function it is checking.
func reveal(t *testing.T, obscured string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(obscured)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) < aes.BlockSize {
		t.Fatalf("obscured value is shorter than a block: %d bytes", len(raw))
	}
	block, err := aes.NewCipher(rcloneObscureKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	iv, ciphertext := raw[:aes.BlockSize], raw[aes.BlockSize:]
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext, ciphertext)
	return string(ciphertext)
}

func TestObscurePasswordRoundTrips(t *testing.T) {
	for _, plain := range []string{"", "a", "hello-world", "0123456789abcdef", "Ymy0hg37bUWlnXwH8tTXwT2AQvSfXnfi4rufpA7NbEP48nG8V6LewNOLNZPg8XtjnVEyKVCQ"} {
		obscured, err := obscurePassword(plain)
		if err != nil {
			t.Fatalf("obscurePassword(%q): %v", plain, err)
		}
		if got := reveal(t, obscured); got != plain {
			t.Errorf("round trip of %q = %q", plain, got)
		}
	}
}

// Values produced by the real `rclone obscure` (v1.75.1) must reveal correctly
// with our implementation: if the algorithm ever drifts from rclone's, this
// fails rather than producing a remote that silently cannot authenticate.
func TestRevealMatchesRealRcloneOutput(t *testing.T) {
	cases := map[string]string{
		"hello-world":      "9reZu1VZnvnmq_l76syqfv1cItYHbWWGsuv_",
		"0123456789abcdef": "4nE4uQ98JoqIuSQO4GNegCywvx1rgpsDECgWnP16A78",
		"":                 "fWFWpU8scuwOF8aC1V9AQA",
	}
	for plain, obscured := range cases {
		if got := reveal(t, obscured); got != plain {
			t.Errorf("reveal(%s) = %q, want %q", obscured, got, plain)
		}
	}
}
