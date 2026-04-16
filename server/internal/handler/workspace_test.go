package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateWorkspace_RejectsReservedSlug(t *testing.T) {
	reserved := []string{
		// Auth flow
		"login",
		"logout",
		"signin",
		"signout",
		"signup",
		"auth",
		"oauth",
		"callback",
		"invite",
		"verify",
		"reset",
		"password",
		"onboarding",

		// Platform routes
		"api",
		"admin",
		"help",
		"about",
		"pricing",
		"docs",
		"support",
		"status",
		"legal",
		"privacy",
		"terms",
		"security",

		// Dashboard segments
		"issues",
		"projects",
		"agents",
		"inbox",
		"my-issues",
		"settings",
		"workspaces",
		"teams",

		// RFC 2142
		"postmaster",
		"abuse",
		"noreply",

		// Hostname confusables
		"mail",
		"static",
		"cdn",

		// Web standards
		"_next",
		"favicon.ico",
		"robots.txt",
		"sitemap.xml",
	}

	for _, slug := range reserved {
		t.Run(slug, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/workspaces", map[string]any{
				"name": fmt.Sprintf("Test %s", slug),
				"slug": slug,
			})
			testHandler.CreateWorkspace(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("slug %q: expected 400, got %d: %s", slug, w.Code, w.Body.String())
			}
		})
	}
}
