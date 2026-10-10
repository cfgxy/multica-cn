package computer

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// NewRuntimeDownloadClient keeps a configured proxy scoped to public runtime
// downloads. An empty setting retains the standard environment proxy behavior.
func NewRuntimeDownloadClient(address string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if address != "" {
		proxy, err := url.Parse(address)
		valid := err == nil && proxy.Hostname() != "" && (proxy.Path == "" || proxy.Path == "/") && proxy.RawQuery == "" && proxy.Fragment == ""
		if valid {
			switch proxy.Scheme {
			case "http", "https", "socks5", "socks5h":
			default:
				valid = false
			}
			if port := proxy.Port(); port != "" {
				n, err := strconv.Atoi(port)
				valid = valid && err == nil && n > 0 && n <= 65535
			}
		}
		transport.Proxy = func(*http.Request) (*url.URL, error) {
			if !valid {
				// Do not include the address: it may contain proxy credentials.
				return nil, errors.New("invalid MULTICA_RUNTIME_DOWNLOAD_PROXY configuration")
			}
			return proxy, nil
		}
	}
	return &http.Client{Transport: transport, Timeout: RuntimeDownloadTimeout}
}
