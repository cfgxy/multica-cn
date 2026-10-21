package credentialcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testContext() Context {
	return Context{
		WorkspaceID:  "workspace-fixture",
		ResourceType: "issue",
		ResourceID:   "issue-fixture",
		SubjectType:  "agent",
		SubjectID:    "agent-fixture",
		Purpose:      "credential.create",
		Version:      ProtocolVersion,
	}
}

func sealTransportFixture(t *testing.T, key *rsa.PublicKey, keyID string, context Context, plaintext []byte) TransportEnvelope {
	t.Helper()
	aad, err := context.AssociatedData()
	if err != nil {
		t.Fatal("context rejected")
	}
	dataKey := make([]byte, dataKeySize)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal("fixture key generation failed")
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		t.Fatal("fixture cipher setup failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal("fixture AEAD setup failed")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal("fixture nonce generation failed")
	}
	label := sha256.Sum256(aad)
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, dataKey, label[:])
	if err != nil {
		t.Fatal("fixture wrapping failed")
	}
	return TransportEnvelope{
		Version:        ProtocolVersion,
		KeyID:          keyID,
		Algorithm:      TransportRSAOAEP,
		WrappedDataKey: base64.RawURLEncoding.EncodeToString(wrapped),
		Nonce:          base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:     base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, aad)),
	}
}

func TestTransportEnvelopeOpenAuthenticatesContextWithoutHTTP(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("test key generation failed")
	}
	ctx := testContext()
	sealed := sealTransportFixture(t, &privateKey.PublicKey, "transport-v1", ctx, []byte("synthetic-fixture"))
	opened, err := (TransportKeyring{
		CurrentKeyID: "transport-v2",
		PrivateKeys: map[string]*rsa.PrivateKey{
			"transport-v1": privateKey,
		},
	}).Open(sealed, ctx)
	if err != nil || string(opened) != "synthetic-fixture" {
		t.Fatal("valid envelope did not open")
	}

	for name, mutate := range map[string]func(*Context){
		"workspace":      func(c *Context) { c.WorkspaceID = "other-workspace" },
		"resource scope": func(c *Context) { c.ResourceType = "project" },
		"resource":       func(c *Context) { c.ResourceID = "other-issue" },
		"subject":        func(c *Context) { c.SubjectID = "other-agent" },
		"purpose":        func(c *Context) { c.Purpose = "credential.replace" },
		"version":        func(c *Context) { c.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := ctx
			mutate(&changed)
			if _, err := (TransportKeyring{PrivateKeys: map[string]*rsa.PrivateKey{"transport-v1": privateKey}}).Open(sealed, changed); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatal("context substitution did not fail closed")
			}
		})
	}
}

func TestDecodeTransportEnvelopeRejectsRawSecretFields(t *testing.T) {
	base := `{"version":1,"key_id":"transport-v1","algorithm":"RSA-OAEP-256+A256GCM","wrapped_data_key":"d2VsbA","nonce":"bm9uY2U","ciphertext":"Y2lwaGVy"}`
	if _, err := DecodeTransportEnvelope([]byte(base)); err != nil {
		t.Fatal("valid encrypted shape rejected")
	}
	for _, field := range []string{"value", "secret", "credential"} {
		t.Run(field, func(t *testing.T) {
			body := strings.TrimSuffix(base, "}") + `,"` + field + `":"synthetic-fixture"}`
			if _, err := DecodeTransportEnvelope([]byte(body)); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatal("raw secret field was not rejected")
			}
		})
	}
	if _, err := DecodeTransportEnvelope([]byte(base + ` {}`)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatal("trailing JSON was not rejected")
	}
	duplicate := strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1)
	if _, err := DecodeTransportEnvelope([]byte(duplicate)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatal("duplicate JSON field was not rejected")
	}
}

func TestTransportTamperingAndUnknownKeyFailWithValueFreeErrors(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("test key generation failed")
	}
	ctx := testContext()
	sealed := sealTransportFixture(t, &privateKey.PublicKey, "transport-v1", ctx, []byte("synthetic-fixture"))
	for name, mutate := range map[string]func(*TransportEnvelope){
		"ciphertext":  func(e *TransportEnvelope) { e.Ciphertext = e.Ciphertext[:len(e.Ciphertext)-1] + "A" },
		"wrapped-key": func(e *TransportEnvelope) { e.WrappedDataKey = e.WrappedDataKey[:len(e.WrappedDataKey)-1] + "A" },
		"version":     func(e *TransportEnvelope) { e.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := sealed
			mutate(&changed)
			_, err := (TransportKeyring{PrivateKeys: map[string]*rsa.PrivateKey{"transport-v1": privateKey}}).Open(changed, ctx)
			if err == nil || strings.Contains(err.Error(), "synthetic-fixture") {
				t.Fatal("tampered envelope did not fail with a value-free error")
			}
		})
	}
	if _, err := (TransportKeyring{PrivateKeys: map[string]*rsa.PrivateKey{}}).Open(sealed, ctx); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("unknown key ID did not fail closed")
	}
}

func TestStorageEnvelopeUsesDistinctDomainAndSupportsKeyRotation(t *testing.T) {
	ctx := testContext()
	aad, err := ctx.AssociatedData()
	if err != nil {
		t.Fatal("context rejected")
	}
	oldMaster := []byte("synthetic-storage-master-key-v1-32bytes")
	newMaster := []byte("synthetic-storage-master-key-v2-32bytes")
	oldKey := deriveStorageKey(oldMaster)
	newKey := deriveStorageKey(newMaster)
	if string(oldKey) == string(oldMaster) || string(oldKey) == string(newKey) {
		t.Fatal("storage derivation domain/key version did not produce a distinct key")
	}
	oldRing := StorageKeyring{CurrentKeyID: "storage-v1", MasterKeys: map[string][]byte{"storage-v1": oldMaster}}
	envelope, err := oldRing.Seal([]byte("synthetic-fixture"), aad)
	if err != nil {
		t.Fatal("storage seal failed")
	}
	rotated := StorageKeyring{CurrentKeyID: "storage-v2", MasterKeys: map[string][]byte{"storage-v1": oldMaster, "storage-v2": newMaster}}
	opened, err := rotated.Open(envelope, aad)
	if err != nil || string(opened) != "synthetic-fixture" {
		t.Fatal("retained old key could not open prior ciphertext")
	}
	changedAAD := append(append([]byte(nil), aad...), []byte("changed")...)
	if _, err := rotated.Open(envelope, changedAAD); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatal("storage AAD substitution did not fail closed")
	}

	encoded, err := json.Marshal(envelope)
	if err != nil || strings.Contains(string(encoded), "synthetic-fixture") {
		t.Fatal("storage envelope exposed fixture plaintext")
	}
}

func TestAuthorizedReencryptionNeverReturnsPlaintext(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("test key generation failed")
	}
	transportContext := testContext()
	transportEnvelope := sealTransportFixture(t, &privateKey.PublicKey, "transport-v1", transportContext, []byte("synthetic-fixture"))
	storageContext := transportContext
	storageContext.ResourceType = "credential"
	storageContext.ResourceID = "credential-fixture"
	storageContext.Purpose = "credential.storage"
	storageAAD, err := storageContext.AssociatedData()
	if err != nil {
		t.Fatal("storage context rejected")
	}
	storageRing := StorageKeyring{CurrentKeyID: "storage-v1", MasterKeys: map[string][]byte{
		"storage-v1": []byte("synthetic-static-storage-master-key-32bytes"),
	}}
	stored, err := ReencryptForStorage(
		TransportKeyring{PrivateKeys: map[string]*rsa.PrivateKey{"transport-v1": privateKey}},
		storageRing,
		transportEnvelope,
		transportContext,
		storageAAD,
	)
	if err != nil {
		t.Fatal("authorized re-encryption failed")
	}
	serialized, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(serialized), "synthetic-fixture") {
		t.Fatal("storage write representation exposed fixture plaintext")
	}
	opened, err := storageRing.Open(stored, storageAAD)
	if err != nil || string(opened) != "synthetic-fixture" {
		t.Fatal("storage envelope could not be opened")
	}
}

func TestKeyringConfigurationFailsClosedAndRetainsRotationKeys(t *testing.T) {
	if _, err := LoadTransportKeyring(""); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("missing transport keyring did not fail closed")
	}
	if _, err := LoadStorageKeyring(""); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("missing storage keyring did not fail closed")
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("test key generation failed")
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal("test key encoding failed")
	}
	encoded := base64.RawURLEncoding.EncodeToString(der)
	transportConfig, err := json.Marshal(map[string]any{
		"current_key_id": "transport-v2",
		"private_keys_pkcs8": map[string]string{
			"transport-v1": encoded,
			"transport-v2": encoded,
		},
	})
	if err != nil {
		t.Fatal("test config encoding failed")
	}
	transport, err := LoadTransportKeyring(string(transportConfig))
	if err != nil || transport.CurrentKeyID != "transport-v2" || len(transport.PrivateKeys) != 2 {
		t.Fatal("valid rotating transport keyring rejected")
	}
	public, err := transport.CurrentPublicKey()
	if err != nil || public.KeyID != "transport-v2" || public.Algorithm != TransportRSAOAEP || public.PublicKeySPKI == "" {
		t.Fatal("current public transport key not exposed")
	}

	storageKey := base64.StdEncoding.EncodeToString([]byte("synthetic-static-storage-master-key-32bytes"))
	storageConfig, err := json.Marshal(map[string]any{
		"current_key_id":     "storage-v1",
		"master_keys_base64": map[string]string{"storage-v1": storageKey},
	})
	if err != nil {
		t.Fatal("test config encoding failed")
	}
	storage, err := LoadStorageKeyring(string(storageConfig))
	if err != nil || storage.CurrentKeyID != "storage-v1" {
		t.Fatal("valid storage keyring rejected")
	}
}
