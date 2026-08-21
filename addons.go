package kiosk

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

func addonsIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		var rows []addonModel
		if err := db.Model(&addonModel{}).OrderBy("name", playsql.Asc).Get(ctx.Request.Context(), &rows); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

func addonsCreate(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *addonForm) {
		if form.dateErr != nil {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": form.dateErr.Error()})
			return
		}

		var row addonModel
		err := db.Model(&addonModel{}).Create(ctx.Request.Context(), &row, map[string]any{
			"uuid":         uuid.NewString(),
			"name":         form.Name,
			"price_cents":  form.PriceCents,
			"currency":     defaultCurrency(form.Currency),
			"active_from":  form.activeFrom,
			"active_until": form.activeUntil,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, row)
	})
}

// companiesAttachAddon handles POST /kiosks/companies/:id/addons.
func companiesAttachAddon(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *attachAddonForm) {
		companyID, ok := paramInt(ctx, "id")
		if !ok {
			return
		}
		_, err := db.Model(&companyAddonModel{}).Insert(ctx.Request.Context(), map[string]any{
			"company_id": companyID,
			"addon_id":   form.AddonID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
	})
}

// companiesDetachAddon handles DELETE /kiosks/companies/:id/addons/:addonId.
func companiesDetachAddon(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		companyID, ok := paramInt(ctx, "id")
		if !ok {
			return
		}
		addonID, ok := paramInt(ctx, "addonId")
		if !ok {
			return
		}
		_, err := db.Model(&companyAddonModel{}).
			WhereEq("company_id", companyID).
			WhereEq("addon_id", addonID).
			ForceDelete(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}
