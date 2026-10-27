package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"
)

const capsuleMagic = "clc-v1:"

type compactCapsule struct {
	Version   int    `json:"v"`
	CreatedAt int64  `json:"ts"`
	Model     string `json:"model,omitempty"`
	Summary   string `json:"summary"`
}

func sealCapsule(summary, model string) (string, error) {
	payload, err := json.Marshal(compactCapsule{
		Version:   1,
		CreatedAt: time.Now().Unix(),
		Model:     model,
		Summary:   summary,
	})
	if err != nil {
		return "", err
	}
	key := capsuleKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, payload, nil)
	return capsuleMagic + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func expandCapsule(token string) (string, bool) {
	if !strings.HasPrefix(token, capsuleMagic) {
		return "", false
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(token, capsuleMagic))
	if err != nil {
		return "", false
	}
	key := capsuleKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", false
	}
	if len(raw) < gcm.NonceSize() {
		return "", false
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", false
	}
	var cap compactCapsule
	if err := json.Unmarshal(plain, &cap); err != nil {
		return "", false
	}
	if strings.TrimSpace(cap.Summary) == "" {
		return "", false
	}
	return cap.Summary, true
}

func capsuleKey() []byte {
	sum := sha256.Sum256([]byte("codex-local-compact|" + pluginIdentifier + "|v1"))
	return sum[:]
}
