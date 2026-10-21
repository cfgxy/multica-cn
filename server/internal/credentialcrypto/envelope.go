package credentialcrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

const (
	ProtocolVersion  = 1
	TransportRSAOAEP = "RSA-OAEP-256+A256GCM"
	StorageAESGCM    = "A256GCM"
	dataKeySize      = 32
)

var (
	ErrInvalidEnvelope = errors.New("credential envelope is invalid")
	ErrKeyUnavailable  = errors.New("credential encryption key is unavailable")
)

// Context is rebuilt from authenticated server-side state before opening an
// envelope. Its serialized form is authenticated as AEAD associated data.
type Context struct {
	WorkspaceID  string `json:"workspace_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	Purpose      string `json:"purpose"`
	Version      int    `json:"version"`
}

func (c Context) AssociatedData() ([]byte, error) {
	if c.WorkspaceID == "" || c.ResourceType == "" || c.ResourceID == "" ||
		c.SubjectType == "" || c.SubjectID == "" || c.Purpose == "" || c.Version != ProtocolVersion {
		return nil, ErrInvalidEnvelope
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return b, nil
}

type TransportEnvelope struct {
	Version        int    `json:"version"`
	KeyID          string `json:"key_id"`
	Algorithm      string `json:"algorithm"`
	WrappedDataKey string `json:"wrapped_data_key"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
}

// TransportKeyring keeps the active public-key ID separate from retained
// private keys so deployments can rotate without invalidating in-flight writes.
type TransportKeyring struct {
	CurrentKeyID string
	PrivateKeys  map[string]*rsa.PrivateKey
}

type PublicTransportKey struct {
	KeyID         string `json:"key_id"`
	Algorithm     string `json:"algorithm"`
	PublicKeySPKI string `json:"public_key_spki"`
}

func (k TransportKeyring) CurrentPublicKey() (PublicTransportKey, error) {
	publicKey, err := k.PublicKey(k.CurrentKeyID)
	if err != nil || k.CurrentKeyID == "" {
		return PublicTransportKey{}, ErrKeyUnavailable
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return PublicTransportKey{}, ErrKeyUnavailable
	}
	return PublicTransportKey{
		KeyID:         k.CurrentKeyID,
		Algorithm:     TransportRSAOAEP,
		PublicKeySPKI: base64.RawURLEncoding.EncodeToString(der),
	}, nil
}

func (k TransportKeyring) PublicKey(keyID string) (*rsa.PublicKey, error) {
	priv := k.PrivateKeys[keyID]
	if priv == nil || priv.N.BitLen() < 2048 {
		return nil, ErrKeyUnavailable
	}
	return &priv.PublicKey, nil
}

func (k TransportKeyring) Open(envelope TransportEnvelope, context Context) ([]byte, error) {
	if envelope.Version != ProtocolVersion || envelope.Algorithm != TransportRSAOAEP ||
		envelope.KeyID == "" || len(envelope.Ciphertext) > 1<<20 {
		return nil, ErrInvalidEnvelope
	}
	key := k.PrivateKeys[envelope.KeyID]
	if key == nil || key.N.BitLen() < 2048 {
		return nil, ErrKeyUnavailable
	}
	aad, err := context.AssociatedData()
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(envelope.WrappedDataKey)
	if err != nil || len(wrapped) != key.Size() {
		return nil, ErrInvalidEnvelope
	}
	label := sha256.Sum256(aad)
	dataKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, wrapped, label[:])
	if err != nil || len(dataKey) != dataKeySize {
		return nil, ErrInvalidEnvelope
	}
	defer zero(dataKey)
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, ErrInvalidEnvelope
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return plaintext, nil
}

type StorageEnvelope struct {
	Version    int    `json:"version"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// StorageKeyring uses independently configured master keys and a distinct
// derivation domain from the asymmetric transport wrapping keys.
type StorageKeyring struct {
	CurrentKeyID string
	MasterKeys   map[string][]byte
}

func (k StorageKeyring) Seal(plaintext []byte, aad []byte) (StorageEnvelope, error) {
	master := k.MasterKeys[k.CurrentKeyID]
	if k.CurrentKeyID == "" || len(master) < 32 {
		return StorageEnvelope{}, ErrKeyUnavailable
	}
	key := deriveStorageKey(master)
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return StorageEnvelope{}, ErrInvalidEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return StorageEnvelope{}, ErrInvalidEnvelope
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return StorageEnvelope{}, errors.New("credential encryption failed")
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	return StorageEnvelope{
		Version:    ProtocolVersion,
		KeyID:      k.CurrentKeyID,
		Algorithm:  StorageAESGCM,
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

func (k StorageKeyring) Open(envelope StorageEnvelope, aad []byte) ([]byte, error) {
	if envelope.Version != ProtocolVersion || envelope.Algorithm != StorageAESGCM || envelope.KeyID == "" {
		return nil, ErrInvalidEnvelope
	}
	master := k.MasterKeys[envelope.KeyID]
	if len(master) < 32 {
		return nil, ErrKeyUnavailable
	}
	key := deriveStorageKey(master)
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, ErrInvalidEnvelope
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return plaintext, nil
}

// ReencryptForStorage must be called only after the service has authorized the
// request context. It never returns the decrypted value to its caller.
func ReencryptForStorage(transport TransportKeyring, storage StorageKeyring, envelope TransportEnvelope, transportContext Context, storageAAD []byte) (StorageEnvelope, error) {
	plaintext, err := transport.Open(envelope, transportContext)
	if err != nil {
		return StorageEnvelope{}, err
	}
	defer zero(plaintext)
	return storage.Seal(plaintext, storageAAD)
}

func deriveStorageKey(master []byte) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("multica/credential/static-storage/aes-256-gcm/v1"))
	return mac.Sum(nil)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// DecodeTransportEnvelope strictly accepts only the encrypted wire shape.
// Unknown fields (including raw secret fields) and trailing JSON fail closed.
func DecodeTransportEnvelope(data []byte) (TransportEnvelope, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return TransportEnvelope{}, ErrInvalidEnvelope
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return TransportEnvelope{}, ErrInvalidEnvelope
	}
	var envelope TransportEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return TransportEnvelope{}, ErrInvalidEnvelope
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return TransportEnvelope{}, ErrInvalidEnvelope
	}
	if envelope.Version != ProtocolVersion || envelope.KeyID == "" || envelope.Algorithm != TransportRSAOAEP ||
		envelope.WrappedDataKey == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return TransportEnvelope{}, ErrInvalidEnvelope
	}
	return envelope, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrInvalidEnvelope
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidEnvelope
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidEnvelope
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return ErrInvalidEnvelope
	}
}
