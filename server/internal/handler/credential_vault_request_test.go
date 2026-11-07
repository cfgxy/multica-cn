package handler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const validCredentialRequestFixture = `{"name":"fixture","purpose":"synthetic test","subject_type":"agent","subject_id":"00000000-0000-7000-8000-000000000001","resource_scope":"issue","resource_id":"00000000-0000-7000-8000-000000000002","envelope":{"version":1,"key_id":"transport-test","algorithm":"RSA-OAEP-256+A256GCM","wrapped_data_key":"d2VsbA","nonce":"bm9uY2U","ciphertext":"Y2lwaGVy"}}`

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

func TestCredentialDeliveryRequestRejectsConnectorFields(t *testing.T) {
	request := strings.Replace(validCredentialRequestFixture,
		`"resource_id":"00000000-0000-7000-8000-000000000002"`,
		`"request_comment_id":"00000000-0000-7000-8000-000000000003"`, 1,
	)
	req, _, err := decodeCreateIssueCredentialRequest([]byte(request))
	if err != nil || !credentialDeliveryRequestValid(req) {
		t.Fatalf("request card payload was rejected: %v", err)
	}
	for name, body := range map[string]string{
		"target service":     strings.Replace(request, `"purpose"`, `"target_service":"service","purpose"`, 1),
		"connector ID":       strings.Replace(request, `"purpose"`, `"connector_id":"00000000-0000-7000-8000-000000000004","purpose"`, 1),
		"connector revision": strings.Replace(request, `"purpose"`, `"connector_revision":1,"purpose"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			candidate, _, decodeErr := decodeCreateIssueCredentialRequest([]byte(body))
			if decodeErr != nil {
				t.Fatalf("request decoding failed: %v", decodeErr)
			}
			if credentialDeliveryRequestValid(candidate) {
				t.Fatal("request card payload accepted a prohibited field")
			}
		})
	}
}

func TestCredentialConnectorCompatibilityFieldsRemainSupported(t *testing.T) {
	legacy := createIssueCredentialRequest{
		Name: "fixture", TargetService: "legacy-service", Purpose: "synthetic test",
		ConnectorID: "00000000-0000-7000-8000-000000000003", ConnectorRev: 2,
	}
	connectorID, usesConnector, err := credentialRequestConnector(legacy)
	if err != nil || !usesConnector || uuidToString(connectorID) != legacy.ConnectorID {
		t.Fatalf("legacy connector request = (%v, %t, %v)", connectorID, usesConnector, err)
	}
	if !credentialRequestValid(legacy) || credentialTargetService(legacy) != "legacy-service" {
		t.Fatal("legacy connector request was not preserved")
	}

	direct := legacy
	direct.TargetService = ""
	direct.ConnectorID = ""
	direct.ConnectorRev = 0
	_, usesConnector, err = credentialRequestConnector(direct)
	if err != nil || usesConnector || credentialTargetService(direct) != direct.Name {
		t.Fatalf("direct request = (uses_connector=%t, target_service=%q, err=%v)", usesConnector, credentialTargetService(direct), err)
	}

	response, err := json.Marshal(credentialMetadataResponse{
		ID: "credential", ConnectorID: "connector", ConnectorRevision: 1,
	})
	if err != nil || !strings.Contains(string(response), `"connector_id":"connector"`) || !strings.Contains(string(response), `"connector_revision":1`) {
		t.Fatalf("credential response lost legacy connector metadata: %s (%v)", response, err)
	}
}
