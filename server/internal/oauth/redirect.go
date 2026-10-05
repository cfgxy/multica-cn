package oauth

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateRedirectURIs rejects what the authorization endpoint could never
// match anyway. redirectURIAllowed compares registered entries byte for
// byte, so a relative or fragment-carrying entry is dead weight that only
// surfaces as an opaque "redirect_uri is not registered" at authorize time.
//
// Shared by the oauth_client CLI and the System Settings management API
// (RUYI-420): one validation, so a redirect_uri the UI accepts is one the
// CLI accepts and both behave identically at authorize time.
func ValidateRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return fmt.Errorf("at least one redirect_uri is required")
	}
	if len(uris) > MaxRedirectURIs {
		return fmt.Errorf("at most %d redirect_uri values are allowed", MaxRedirectURIs)
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid redirect_uri %q: %w", raw, err)
		}
		if !u.IsAbs() {
			return fmt.Errorf("redirect_uri %q must be absolute", raw)
		}
		if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("redirect_uri %q must use https (localhost may use http)", raw)
		}
		if u.Fragment != "" || strings.Contains(raw, "#") {
			return fmt.Errorf("redirect_uri %q must not carry a fragment", raw)
		}
	}
	return nil
}

// MaxRedirectURIs caps one client's registered list. Registered entries are
// exact-matched, so a useful list is a handful of deploy-specific URLs.
const MaxRedirectURIs = 20
