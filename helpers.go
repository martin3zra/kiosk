package kiosk

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
)

// paramUUID reads a required uuid path param, writing a 400 response and
// returning ok=false if it's missing or not a well-formed uuid. Every
// URL-facing id in Kiosk's API is a uuid — internal integer primary keys
// never appear in a path, only in foreign-key columns and request bodies.
func paramUUID(ctx *routing.Context, name string) (string, bool) {
	v := ctx.Params[name]
	if _, err := uuid.Parse(v); err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]any{"error": "invalid " + name})
		return "", false
	}
	return v, true
}

func defaultCurrency(currency string) string {
	if currency == "" {
		return "USD"
	}
	return currency
}
