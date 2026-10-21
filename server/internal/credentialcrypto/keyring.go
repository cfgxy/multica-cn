package credentialcrypto

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

type transportKeyringConfig struct {
	CurrentKeyID string            `json:"current_key_id"`
	PrivateKeys  map[string]string `json:"private_keys_pkcs8"`
}

type storageKeyringConfig struct {
	CurrentKeyID string            `json:"current_key_id"`
	MasterKeys   map[string]string `json:"master_keys_base64"`
}

func LoadTransportKeyring(encoded string) (TransportKeyring, error) {
	var config transportKeyringConfig
	if err := decodeKeyringConfig(encoded, &config); err != nil || config.CurrentKeyID == "" {
		return TransportKeyring{}, ErrKeyUnavailable
	}
	keys := make(map[string]*rsa.PrivateKey, len(config.PrivateKeys))
	for keyID, encodedKey := range config.PrivateKeys {
		if keyID == "" {
			return TransportKeyring{}, ErrKeyUnavailable
		}
		der, err := base64.RawURLEncoding.DecodeString(encodedKey)
		if err != nil {
			return TransportKeyring{}, ErrKeyUnavailable
		}
		parsed, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return TransportKeyring{}, ErrKeyUnavailable
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok || key.N.BitLen() < 2048 || key.Validate() != nil {
			return TransportKeyring{}, ErrKeyUnavailable
		}
		keys[keyID] = key
	}
	if keys[config.CurrentKeyID] == nil {
		return TransportKeyring{}, ErrKeyUnavailable
	}
	return TransportKeyring{CurrentKeyID: config.CurrentKeyID, PrivateKeys: keys}, nil
}

func LoadStorageKeyring(encoded string) (StorageKeyring, error) {
	var config storageKeyringConfig
	if err := decodeKeyringConfig(encoded, &config); err != nil || config.CurrentKeyID == "" {
		return StorageKeyring{}, ErrKeyUnavailable
	}
	keys := make(map[string][]byte, len(config.MasterKeys))
	for keyID, encodedKey := range config.MasterKeys {
		if keyID == "" {
			return StorageKeyring{}, ErrKeyUnavailable
		}
		key, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil || len(key) < 32 {
			return StorageKeyring{}, ErrKeyUnavailable
		}
		keys[keyID] = key
	}
	if len(keys[config.CurrentKeyID]) < 32 {
		return StorageKeyring{}, ErrKeyUnavailable
	}
	return StorageKeyring{CurrentKeyID: config.CurrentKeyID, MasterKeys: keys}, nil
}

func decodeKeyringConfig(encoded string, out any) error {
	if encoded == "" || len(encoded) > 1<<20 {
		return ErrKeyUnavailable
	}
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return ErrKeyUnavailable
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrKeyUnavailable
	}
	return nil
}
