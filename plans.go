package kiosk

import (
	"net/http"

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
		var row planModel
		err := db.Model(&planModel{}).Create(ctx.Request.Context(), &row, map[string]any{
			"uuid":        uuid.NewString(),
			"name":        form.Name,
			"price_cents": form.PriceCents,
			"currency":    defaultCurrency(form.Currency),
			"limits":      form.Limits,
		})
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
		_, err := db.Model(&planModel{}).WhereEq("uuid", id).Update(ctx.Request.Context(), map[string]any{
			"name":        form.Name,
			"price_cents": form.PriceCents,
			"currency":    defaultCurrency(form.Currency),
			"limits":      form.Limits,
		})
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
