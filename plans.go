package kiosk

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

func plansIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		var rows []planModel
		if err := db.Model(&planModel{}).OrderBy("name", playsql.Asc).Get(ctx.Request.Context(), &rows); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

func plansCreate(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *planForm) {
		values := map[string]any{
			"uuid":        uuid.NewString(),
			"name":        form.Name,
			"price_cents": form.PriceCents,
			"currency":    defaultCurrency(form.Currency),
			"limits":      form.Limits,
		}
		// Only written when sent, so a host that hasn't run the plans.code
		// migration yet keeps working.
		if form.Code != nil {
			values["code"] = planCode(form.Code)
		}
		var row planModel
		err := db.Model(&planModel{}).Create(ctx.Request.Context(), &row, values)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, row)
	})
}

func plansUpdate(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *planForm) {
		id, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		values := map[string]any{
			"name":        form.Name,
			"price_cents": form.PriceCents,
			"currency":    defaultCurrency(form.Currency),
			"limits":      form.Limits,
		}
		if form.Code != nil {
			values["code"] = planCode(form.Code)
		}
		_, err := db.Model(&planModel{}).WhereEq("uuid", id).Update(ctx.Request.Context(), values)
		if err != nil {
			ctx.Error(err)
			return
		}
		var row planModel
		if err := db.Model(&planModel{}).WhereEq("uuid", id).First(ctx.Request.Context(), &row); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, row)
	})
}

// planCode normalizes a submitted code: trimmed and lowercased, and an empty
// string clears it (NULL), since the column is unique when set.
func planCode(code *string) any {
	c := strings.ToLower(strings.TrimSpace(*code))
	if c == "" {
		return nil
	}
	return c
}
