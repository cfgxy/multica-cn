package credentialcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const ExecutionEnvelopeAlgorithm = "X25519-HKDF-SHA256+A256GCM"

var ErrInvalidExecutionEnvelope = errors.New("credential execution envelope is invalid")

type ExecutionEnvelope struct {
	Version            int    `json:"version"`
	Algorithm          string `json:"algorithm"`
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

func NewSessionKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

func SealForSession(publicKey, plaintext, aad []byte) (ExecutionEnvelope, error) {
	if len(publicKey) != 32 || len(plaintext) == 0 || len(aad) == 0 {
		return ExecutionEnvelope{}, ErrInvalidExecutionEnvelope
	}
	curve := ecdh.X25519()
	peer, err := curve.NewPublicKey(publicKey)
	if err != nil {
		return ExecutionEnvelope{}, ErrInvalidExecutionEnvelope
	}
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return ExecutionEnvelope{}, errors.New("credential execution encryption failed")
	}
	shared, err := ephemeral.ECDH(peer)
	if err != nil {
		return ExecutionEnvelope{}, ErrInvalidExecutionEnvelope
	}
	defer zero(shared)
	key := deriveExecutionKey(shared, aad)
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ExecutionEnvelope{}, ErrInvalidExecutionEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ExecutionEnvelope{}, ErrInvalidExecutionEnvelope
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ExecutionEnvelope{}, errors.New("credential execution encryption failed")
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	return ExecutionEnvelope{
		Version: ProtocolVersion, Algorithm: ExecutionEnvelopeAlgorithm,
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:              base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

func OpenForSession(privateKey *ecdh.PrivateKey, envelope ExecutionEnvelope, aad []byte) ([]byte, error) {
	if privateKey == nil || envelope.Version != ProtocolVersion || envelope.Algorithm != ExecutionEnvelopeAlgorithm || len(aad) == 0 {
		return nil, ErrInvalidExecutionEnvelope
	}
	curve := ecdh.X25519()
	ephBytes, err := base64.RawURLEncoding.Strict().DecodeString(envelope.EphemeralPublicKey)
	if err != nil || len(ephBytes) != 32 {
		return nil, ErrInvalidExecutionEnvelope
	}
	eph, err := curve.NewPublicKey(ephBytes)
	if err != nil {
		return nil, ErrInvalidExecutionEnvelope
	}
	shared, err := privateKey.ECDH(eph)
	if err != nil {
		return nil, ErrInvalidExecutionEnvelope
	}
	defer zero(shared)
	key := deriveExecutionKey(shared, aad)
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidExecutionEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidExecutionEnvelope
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, ErrInvalidExecutionEnvelope
	}
	ciphertext, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) > 1<<20 {
		return nil, ErrInvalidExecutionEnvelope
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrInvalidExecutionEnvelope
	}
	return plaintext, nil
}

func deriveExecutionKey(shared, aad []byte) []byte {
	salt := sha256.Sum256(aad)
	extract := hmac.New(sha256.New, salt[:])
	_, _ = extract.Write(shared)
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	_, _ = expand.Write([]byte("multica/credential/execution-envelope/aes-256-gcm/v1"))
	_, _ = expand.Write([]byte{1})
	return expand.Sum(nil)[:32]
}
