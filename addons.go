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

// companiesAttachAddon handles POST /kiosks/companies/:id/addons. :id is a
// uuid (every URL-facing id in Kiosk's API is), but company_addons stores
// integer FKs, so the company row is resolved first to get its internal id.
func companiesAttachAddon(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *attachAddonForm) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		var company companyModel
		if err := db.Model(&companyModel{}).WhereEq("uuid", companyUUID).First(ctx.Request.Context(), &company); err != nil {
			ctx.Error(err)
			return
		}
		_, err := db.Model(&companyAddonModel{}).Insert(ctx.Request.Context(), map[string]any{
			"company_id": company.ID,
			"addon_id":   form.AddonID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
	})
}

// companiesDetachAddon handles DELETE /kiosks/companies/:id/addons/:addonId
// — both uuids, resolved to their internal ids before matching them against
// company_addons' integer FK columns.
func companiesDetachAddon(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		addonUUID, ok := paramUUID(ctx, "addonId")
		if !ok {
			return
		}
		var company companyModel
		if err := db.Model(&companyModel{}).WhereEq("uuid", companyUUID).First(ctx.Request.Context(), &company); err != nil {
			ctx.Error(err)
			return
		}
		var addon addonModel
		if err := db.Model(&addonModel{}).WhereEq("uuid", addonUUID).First(ctx.Request.Context(), &addon); err != nil {
			ctx.Error(err)
			return
		}
		_, err := db.Model(&companyAddonModel{}).
			WhereEq("company_id", company.ID).
			WhereEq("addon_id", addon.ID).
			ForceDelete(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}
