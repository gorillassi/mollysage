package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

type CryptoConfig struct {
	ArgonTime    uint32
	ArgonMemory  uint32
	ArgonThreads uint8
	ArgonKeyLen  uint32
}

var defaultCryptoConfig = CryptoConfig{
	ArgonTime:    1,
	ArgonMemory:  64 * 1024,
	ArgonThreads: 4,
	ArgonKeyLen:  32,
}

func generateRandomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func deriveKeyFromPassword(password string, salt []byte, cfg CryptoConfig) []byte {
	return argon2.IDKey([]byte(password), salt, cfg.ArgonTime, cfg.ArgonMemory, cfg.ArgonThreads, cfg.ArgonKeyLen)
}

func aesGCMEncrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce, err = generateRandomBytes(aesgcm.NonceSize())
	if err != nil {
		return nil, nil, err
	}
	ciphertext = aesgcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func aesGCMDecrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

type UserKeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

func generateUserKeyPair() (*UserKeyPair, error) {
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	pub := priv.PublicKey()
	return &UserKeyPair{
		PublicKey:  pub.Bytes(),
		PrivateKey: priv.Bytes(),
	}, nil
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func decodeBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty base64 string")
	}
	return base64.StdEncoding.DecodeString(s)
}

func deriveSessionKeyFromX25519(privateKeyBytes, peerPublicKeyBytes []byte) ([]byte, error) {
	curve := ecdh.X25519()

	priv, err := curve.NewPrivateKey(privateKeyBytes)
	if err != nil {
		return nil, err
	}
	pub, err := curve.NewPublicKey(peerPublicKeyBytes)
	if err != nil {
		return nil, err
	}

	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, err
	}

	h := hkdf.New(sha256.New, shared, nil, []byte("chat-session"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(h, key); err != nil {
		return nil, err
	}
	return key, nil
}

// Клиентские удобные функции: E2E-шифрование и дешифрование

func EncryptMessageE2E(senderPriv, receiverPub, plaintext []byte) (ciphertext, nonce []byte, err error) {
	sessionKey, err := deriveSessionKeyFromX25519(senderPriv, receiverPub)
	if err != nil {
		return nil, nil, err
	}
	ct, n, err := aesGCMEncrypt(sessionKey, plaintext)
	if err != nil {
		return nil, nil, err
	}
	return ct, n, nil
}

func DecryptMessageE2E(receiverPriv, senderPub, ciphertext, nonce []byte) ([]byte, error) {
	sessionKey, err := deriveSessionKeyFromX25519(receiverPriv, senderPub)
	if err != nil {
		return nil, err
	}
	return aesGCMDecrypt(sessionKey, ciphertext, nonce)
}
