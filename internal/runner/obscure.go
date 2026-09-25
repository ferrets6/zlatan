// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// rcloneObscureKey is rclone's fixed obfuscation key, copied from
// fs/config/obscure/obscure.go. It is not a secret: the "obscuring" rclone
// does is reversible by anyone and exists only to stop a password from being
// shoulder-surfed in a config file. Using the same key is what lets Zlatan
// hand rclone an obscured password through RCLONE_CONFIG_<REMOTE>_PASS
// instead of writing a config file to disk.
var rcloneObscureKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

// obscurePassword renders a plaintext password the way `rclone obscure` does:
// AES-CTR with a random IV prepended, base64url without padding.
//
// The alternative — shelling out to `rclone obscure` — would put the password
// on a command line where any process on the machine could read it from the
// process list. This keeps it in memory.
func obscurePassword(plaintext string) (string, error) {
	block, err := aes.NewCipher(rcloneObscureKey)
	if err != nil {
		return "", fmt.Errorf("building the obscure cipher: %w", err)
	}
	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", fmt.Errorf("generating the obscure IV: %w", err)
	}
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext[aes.BlockSize:], []byte(plaintext))
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}
