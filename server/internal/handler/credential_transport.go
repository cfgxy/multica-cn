package handler

import "net/http"

// GetCredentialTransportKey exposes only the active public wrapping key.
// TLS remains required as defense in depth and for authenticated key delivery.
func (h *Handler) GetCredentialTransportKey(w http.ResponseWriter, _ *http.Request) {
	key, err := h.CredentialTransportKeys.CurrentPublicKey()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "credential encryption is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, key)
}
