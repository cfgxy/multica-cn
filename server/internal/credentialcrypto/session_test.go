package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestExecutionEnvelopeBindsSessionAndContext(t *testing.T) {
	privateKey, err := NewSessionKey()
	if err != nil {
		t.Fatal("generate session key")
	}
	secret := []byte("synthetic-broker-secret")
	aad := []byte("task=task-a;connector=connector-a;revision=3")
	envelope, err := SealForSession(privateKey.PublicKey().Bytes(), secret, aad)
	if err != nil {
		t.Fatal("seal execution envelope")
	}
	got, err := OpenForSession(privateKey, envelope, aad)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open = %q, %v", got, err)
	}
	zero(got)

	if _, err := OpenForSession(privateKey, envelope, []byte("task=task-b;connector=connector-a;revision=3")); err == nil {
		t.Fatal("opened envelope with a different task binding")
	}
	otherKey, err := NewSessionKey()
	if err != nil {
		t.Fatal("generate other session key")
	}
	if _, err := OpenForSession(otherKey, envelope, aad); err == nil {
		t.Fatal("opened envelope with a different session key")
	}
}

func TestExecutionEnvelopeRejectsTampering(t *testing.T) {
	privateKey, err := NewSessionKey()
	if err != nil {
		t.Fatal("generate session key")
	}
	envelope, err := SealForSession(privateKey.PublicKey().Bytes(), []byte("synthetic-secret"), []byte("binding"))
	if err != nil {
		t.Fatal("seal execution envelope")
	}
	envelope.Ciphertext = flipFirstBase64Character(envelope.Ciphertext)
	if _, err := OpenForSession(privateKey, envelope, []byte("binding")); err == nil {
		t.Fatal("opened tampered envelope")
	}
}
