package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/credentialcrypto"
)

func TestGetCredentialTransportKeyPublishesOnlyCurrentPublicKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("test key generation failed")
	}
	h := &Handler{CredentialTransportKeys: credentialcrypto.TransportKeyring{
		CurrentKeyID: "transport-v2",
		PrivateKeys:  map[string]*rsa.PrivateKey{"transport-v1": privateKey, "transport-v2": privateKey},
	}}
	recorder := httptest.NewRecorder()
	h.GetCredentialTransportKey(recorder, httptest.NewRequest("GET", "/api/credentials/transport-key", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}
	var response credentialcrypto.PublicTransportKey
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal("public key response was not valid JSON")
	}
	if response.KeyID != "transport-v2" || response.PublicKeySPKI == "" || response.Algorithm != credentialcrypto.TransportRSAOAEP {
		t.Fatal("response did not identify the active public key")
	}
	if strings.Contains(recorder.Body.String(), "private") {
		t.Fatal("private key material appeared in response")
	}
}

func TestGetCredentialTransportKeyFailsClosedWithoutConfiguration(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).GetCredentialTransportKey(recorder, httptest.NewRequest("GET", "/api/credentials/transport-key", nil))
	if recorder.Code != 503 || strings.Contains(recorder.Body.String(), "keyring") {
		t.Fatal("missing key configuration did not fail closed with a safe response")
	}
}
