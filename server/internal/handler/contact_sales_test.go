package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newContactSalesRequest(body CreateContactSalesRequest) *http.Request {
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest("POST", "/api/contact-sales", &buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func validContactSalesRequest() CreateContactSalesRequest {
	return CreateContactSalesRequest{
		FirstName:       "Ada",
		LastName:        "Lovelace",
		BusinessEmail:   "ada@analytical-engine.example",
		CompanyName:     "Analytical Engine Co.",
		CompanySize:     "11-50",
		CountryRegion:   "United Kingdom",
		UseCase:         "evaluate",
		Goals:           "We want to compound agent productivity across the team.",
		ConsentOutreach: true,
		ConsentUpdates:  false,
	}
}

func clearContactSalesForEmail(t *testing.T, email string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM contact_sales_inquiry WHERE business_email = $1`, email); err != nil {
		t.Fatalf("clear contact_sales_inquiry: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM contact_sales_inquiry WHERE business_email = $1`, email)
	})
}

func TestCreateContactSalesHappyPath(t *testing.T) {
	body := validContactSalesRequest()
	clearContactSalesForEmail(t, body.BusinessEmail)

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp ContactSalesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected inquiry id in response")
	}
}

func TestCreateContactSalesRejectsFreeEmail(t *testing.T) {
	body := validContactSalesRequest()
	body.BusinessEmail = "ada@gmail.com"

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateContactSalesRejectsInvalidEmail(t *testing.T) {
	body := validContactSalesRequest()
	body.BusinessEmail = "not-an-email"

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateContactSalesRejectsUnknownCompanySize(t *testing.T) {
	body := validContactSalesRequest()
	body.CompanySize = "ten-ish"

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateContactSalesRejectsUnknownUseCase(t *testing.T) {
	body := validContactSalesRequest()
	body.UseCase = "world-domination"

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateContactSalesMissingFirstName(t *testing.T) {
	body := validContactSalesRequest()
	body.FirstName = "   "

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateContactSalesPerEmailRateLimit(t *testing.T) {
	body := validContactSalesRequest()
	body.BusinessEmail = "ratelimit-ada@analytical-engine.example"
	clearContactSalesForEmail(t, body.BusinessEmail)

	for i := 0; i < contactSalesHourlyEmailCap; i++ {
		w := httptest.NewRecorder()
		testHandler.CreateContactSales(w, newContactSalesRequest(body))
		if w.Code != http.StatusCreated {
			t.Fatalf("iteration %d: expected 201, got %d: %s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	testHandler.CreateContactSales(w, newContactSalesRequest(body))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIsBusinessEmailDomain(t *testing.T) {
	cases := []struct {
		email string
		want  bool
	}{
		{"ada@multica.ai", true},
		{"ada@example.com", true},
		{"ada@gmail.com", false},
		{"ada@Gmail.COM", false},
		{"ada@yahoo.co.uk", false},
		{"ada@qq.com", false},
		{"weird-no-at", false},
		{"ada@", false},
	}
	for _, c := range cases {
		got := isBusinessEmailDomain(c.email)
		if got != c.want {
			t.Errorf("isBusinessEmailDomain(%q) = %v, want %v", c.email, got, c.want)
		}
	}
}
