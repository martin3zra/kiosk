package kiosk

import (
	"net/http"
	"strconv"

	"github.com/martin3zra/forge/routing"
)

// paramInt reads a required integer path param, writing a 400 response and
// returning ok=false if it's missing or not a number.
func paramInt(ctx *routing.Context, name string) (int, bool) {
	v, err := strconv.Atoi(ctx.Params[name])
	if err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]any{"error": "invalid " + name})
		return 0, false
	}
	return v, true
}

func defaultCurrency(currency string) string {
	if currency == "" {
		return "USD"
	}
	return currency
}
