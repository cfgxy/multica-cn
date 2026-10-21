package handler

import (
	"errors"
	"strings"
	"testing"
)

const validCredentialRequestFixture = `{"name":"fixture","target_service":"fixture","purpose":"synthetic test","subject_type":"agent","subject_id":"00000000-0000-7000-8000-000000000001","resource_scope":"issue","resource_id":"00000000-0000-7000-8000-000000000002","envelope":{"version":1,"key_id":"transport-test","algorithm":"RSA-OAEP-256+A256GCM","wrapped_data_key":"d2VsbA","nonce":"bm9uY2U","ciphertext":"Y2lwaGVy"}}`

func TestDecodeCreateIssueCredentialRequestAcceptsOnlyEncryptedEnvelope(t *testing.T) {
	if _, _, err := decodeCreateIssueCredentialRequest([]byte(validCredentialRequestFixture)); err != nil {
		t.Fatal("well-shaped encrypted credential request was rejected")
	}

	for name, body := range map[string]string{
		"top-level raw value": strings.TrimSuffix(validCredentialRequestFixture, "}") + `,"value":"synthetic-fixture"}`,
		"top-level secret":    strings.TrimSuffix(validCredentialRequestFixture, "}") + `,"secret":"synthetic-fixture"}`,
		"envelope raw value":  strings.Replace(validCredentialRequestFixture, `"ciphertext":"Y2lwaGVy"}`, `"ciphertext":"Y2lwaGVy","value":"synthetic-fixture"}`, 1),
		"missing envelope":    strings.Replace(validCredentialRequestFixture, `,"envelope":{"version":1,"key_id":"transport-test","algorithm":"RSA-OAEP-256+A256GCM","wrapped_data_key":"d2VsbA","nonce":"bm9uY2U","ciphertext":"Y2lwaGVy"}`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeCreateIssueCredentialRequest([]byte(body))
			if !errors.Is(err, errInvalidCredentialRequest) {
				t.Fatal("request without a strict encrypted envelope was not rejected")
			}
			if strings.Contains(err.Error(), "synthetic-fixture") {
				t.Fatal("request error disclosed fixture value")
			}
		})
	}
}
