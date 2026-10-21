package handler

import "testing"

func TestCredentialConnectorTargetIsExactHTTPSWithoutUserInfoQueryOrFragment(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "https endpoint", url: "https://connector.example/mcp", want: true},
		{name: "http is refused", url: "http://connector.example/mcp"},
		{name: "embedded credentials are refused", url: "https://user:secret@connector.example/mcp"},
		{name: "query routing is refused", url: "https://connector.example/mcp?token=synthetic"},
		{name: "empty query delimiter is refused", url: "https://connector.example/mcp?"},
		{name: "fragment is refused", url: "https://connector.example/mcp#route"},
		{name: "empty fragment delimiter is refused", url: "https://connector.example/mcp#"},
		{name: "surrounding whitespace is refused", url: " https://connector.example/mcp "},
		{name: "hostless target is refused", url: "https:/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validCredentialConnectorTarget(tt.url); got != tt.want {
				t.Fatalf("validCredentialConnectorTarget(%q) = %t, want %t", tt.url, got, tt.want)
			}
		})
	}
}
