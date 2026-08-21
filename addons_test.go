package kiosk

import (
	"net/http"
	"testing"
)

func TestAddonsCreateDateValidation(t *testing.T) {
	db := testDB(t)

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "no dates",
			body:       `{"name":"Basic","price_cents":100,"currency":"USD"}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "valid window",
			body:       `{"name":"Seasonal","price_cents":100,"currency":"USD","active_from":"2026-01-01T00:00:00Z","active_until":"2026-06-01T00:00:00Z"}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "inverted window rejected by PassedValidation",
			body:       `{"name":"Inverted","price_cents":100,"currency":"USD","active_from":"2026-06-01T00:00:00Z","active_until":"2026-01-01T00:00:00Z"}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "unparsable date rejected by PrepareForValidation",
			body:       `{"name":"Bad date","price_cents":100,"currency":"USD","active_from":"not-a-date"}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSON(t, addonsCreate(db), http.MethodPost, tc.body, nil)
			if status != tc.wantStatus {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
}
