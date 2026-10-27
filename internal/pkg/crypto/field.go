// Package crypto 提供敏感字段（手机号等）的字段级加密。
// 用 AES-256-GCM，密钥来自 FIELD_ENC_KEY（base64 编码的 32 字节）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// FieldCipher 字段加解密器。
type FieldCipher struct {
	aead cipher.AEAD
}

// NewFieldCipher 从 base64 密钥构造。
func NewFieldCipher(keyB64 string) (*FieldCipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("FIELD_ENC_KEY 必须是 base64 编码的 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &FieldCipher{aead: aead}, nil
}

// Encrypt 加密明文，返回 nonce||ciphertext。
func (c *FieldCipher) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt 解密密文（nonce||ciphertext）。
func (c *FieldCipher) Decrypt(enc []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(enc) < ns {
		return nil, errors.New("密文长度不足")
	}
	return c.aead.Open(nil, enc[:ns], enc[ns:], nil)
}
