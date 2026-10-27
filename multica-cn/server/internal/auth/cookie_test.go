package auth

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsSecureCookie(t *testing.T) {
	cases := []struct {
		name           string
		requestScheme  string
		forwardedProto string
		remoteAddr     string
		trustedProxies string
		origin         string
		frontendOrigin string
		want           bool
	}{
		{name: "https request remains Secure", requestScheme: "https", frontendOrigin: "http://ignored.example", want: true},
		{name: "http request is not Secure despite https environment", requestScheme: "http", frontendOrigin: "https://ignored.example", want: false},
		{name: "HTTP origin overrides stale trusted HTTPS proxy header", requestScheme: "http", forwardedProto: "https", remoteAddr: "10.0.0.2:443", trustedProxies: "10.0.0.0/8", origin: "http://arbitrary-host.example", want: false},
		{name: "HTTPS origin overrides internal HTTP proxy header", requestScheme: "http", forwardedProto: "http", remoteAddr: "10.0.0.2:443", trustedProxies: "10.0.0.0/8", origin: "https://arbitrary-host.example", want: true},
		{name: "invalid origin falls back to trusted proxy", requestScheme: "http", forwardedProto: "https", remoteAddr: "10.0.0.2:443", trustedProxies: "10.0.0.0/8", origin: "http://arbitrary-host.example/path", want: true},
		{name: "trusted proxy forwarded https", requestScheme: "http", forwardedProto: "https", remoteAddr: "10.0.0.2:443", trustedProxies: "10.0.0.0/8", want: true},
		{name: "untrusted forwarded https is ignored", requestScheme: "http", forwardedProto: "https", remoteAddr: "192.0.2.2:443", trustedProxies: "10.0.0.0/8", want: false},
		{name: "trusted proxy forwarded http", requestScheme: "http", forwardedProto: "http", remoteAddr: "10.0.0.2:443", trustedProxies: "10.0.0.0/8", want: false},
		{name: "empty forwarded protocol", requestScheme: "http", frontendOrigin: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FRONTEND_ORIGIN", tc.frontendOrigin)
			t.Setenv("MULTICA_TRUSTED_PROXIES", tc.trustedProxies)
			req := httptest.NewRequest("GET", tc.requestScheme+"://app.example.com", nil)
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			if tc.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := isSecureCookie(req); got != tc.want {
				t.Errorf("isSecureCookie() = %v, want %v (scheme=%q, X-Forwarded-Proto=%q)", got, tc.want, tc.requestScheme, tc.forwardedProto)
			}
		})
	}
}

func TestAuthCookieRoundTripUsesBrowserProtocol(t *testing.T) {
	t.Setenv("COOKIE_DOMAIN", "")
	t.Setenv("MULTICA_TRUSTED_PROXIES", "127.0.0.0/8")
	for _, tc := range []struct {
		name           string
		secure         bool
		forwardedProto string
	}{
		{name: "HTTP", secure: false, forwardedProto: "https"},
		{name: "HTTPS", secure: true, forwardedProto: "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originScheme := "http"
			if tc.secure {
				originScheme = "https"
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
				if err := SetAuthCookies(w, r, "round-trip-token"); err != nil {
					t.Errorf("SetAuthCookies: %v", err)
					http.Error(w, "cookie error", http.StatusInternalServerError)
				}
			})
			mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie(AuthCookieName)
				if err != nil || cookie.Value != "round-trip-token" {
					http.Error(w, "missing auth cookie", http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			server := httptest.NewUnstartedServer(mux)
			if tc.secure {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := server.Client()
			client.Jar = jar
			loginURL := server.URL + "/login"
			req, err := http.NewRequest(http.MethodPost, loginURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", fmt.Sprintf("%s://arbitrary-host.example", originScheme))
			req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			for _, cookie := range resp.Cookies() {
				if cookie.Name == AuthCookieName && cookie.Secure != tc.secure {
					t.Errorf("auth cookie Secure = %v, want %v", cookie.Secure, tc.secure)
				}
			}

			resp, err = client.Get(server.URL + "/protected")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("protected request status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
		})
	}
}

func TestCookieDomain(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"real domain", ".example.com", ".example.com"},
		{"bare domain", "example.com", "example.com"},
		{"IPv4 rejected", "192.168.5.5", ""},
		{"IPv4 with leading dot rejected", ".192.168.5.5", ""},
		{"IPv6 rejected", "::1", ""},
		{"IPv6 bracketed is not a valid IP literal → passthrough", "[::1]", "[::1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COOKIE_DOMAIN", tc.env)
			if got := cookieDomain(); got != tc.want {
				t.Errorf("cookieDomain() = %q, want %q (COOKIE_DOMAIN=%q)", got, tc.want, tc.env)
			}
		})
	}
}

// TestSetAuthCookies_HTTPSelfHost covers the exact misconfiguration that
// shipped to users on LAN self-host: COOKIE_DOMAIN=<ip> + HTTP FRONTEND_ORIGIN.
// The cookie must land with no Domain attribute and Secure=false so browsers
// actually store it.
func TestSetAuthCookies_HTTPSelfHost(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "http://192.168.5.5:13000")
	t.Setenv("COOKIE_DOMAIN", "192.168.5.5")

	rec := httptest.NewRecorder()
	if err := SetAuthCookies(rec, httptest.NewRequest("POST", "http://192.168.5.5:13000/api/auth/login", nil), "test-token"); err != nil {
		t.Fatalf("SetAuthCookies: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies (auth + csrf), got %d", len(cookies))
	}
	for _, c := range cookies {
		if c.Secure {
			t.Errorf("cookie %q has Secure=true on HTTP origin; browser would reject it", c.Name)
		}
		if c.Domain != "" {
			t.Errorf("cookie %q has Domain=%q; IP-address Domain would be rejected by the browser (RFC 6265)", c.Name, c.Domain)
		}
	}
}

func TestParseAuthTokenTTL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantDur time.Duration
		wantOK  bool
	}{
		{"empty string", "", 0, false},
		{"valid 3600", "3600", time.Hour, true},
		{"valid 86400", "86400", 24 * time.Hour, true},
		{"negative", "-100", 0, false},
		{"zero", "0", 0, false},
		{"non-numeric", "abc", 0, false},
		{"whitespace trimmed", " 7200 ", 2 * time.Hour, true},
		{"duration hours", "8760h", 8760 * time.Hour, true},
		{"duration compound", "720h30m", 720*time.Hour + 30*time.Minute, true},
		{"duration minutes", "90m", 90 * time.Minute, true},
		{"duration negative", "-1h", 0, false},
		{"duration zero", "0s", 0, false},
		{"integer overflow", "9999999999", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAuthTokenTTL(tc.raw)
			if ok != tc.wantOK || got != tc.wantDur {
				t.Errorf("parseAuthTokenTTL(%q) = (%v, %v), want (%v, %v)", tc.raw, got, ok, tc.wantDur, tc.wantOK)
			}
		})
	}
}

func TestSetAuthCookies_HTTPSProduction(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://app.example.com")
	t.Setenv("COOKIE_DOMAIN", "app.example.com")

	rec := httptest.NewRecorder()
	if err := SetAuthCookies(rec, httptest.NewRequest("POST", "https://app.example.com/api/auth/login", nil), "test-token"); err != nil {
		t.Fatalf("SetAuthCookies: %v", err)
	}

	for _, c := range rec.Result().Cookies() {
		if !c.Secure {
			t.Errorf("cookie %q missing Secure flag on HTTPS origin", c.Name)
		}
		if c.Domain != "app.example.com" {
			t.Errorf("cookie %q Domain = %q, want %q", c.Name, c.Domain, "app.example.com")
		}
	}
}

func TestClearAuthCookies_UsesRequestScheme(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://app.example.com")
	for _, tc := range []struct {
		name   string
		scheme string
		secure bool
	}{
		{name: "HTTP", scheme: "http", secure: false},
		{name: "HTTPS", scheme: "https", secure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ClearAuthCookies(rec, httptest.NewRequest("POST", tc.scheme+"://app.example.com/api/auth/logout", nil))

			cookies := rec.Result().Cookies()
			if len(cookies) != 2 {
				t.Fatalf("expected 2 cleared cookies, got %d", len(cookies))
			}
			for _, cookie := range cookies {
				if cookie.Secure != tc.secure {
					t.Errorf("cookie %q Secure = %v, want %v", cookie.Name, cookie.Secure, tc.secure)
				}
			}
		})
	}
}
