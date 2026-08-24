package kiosk

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

func companiesIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		var rows []companyModel
		if err := db.Model(&companyModel{}).OrderBy("name", playsql.Asc).Get(ctx.Request.Context(), &rows); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

func companiesShow(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		id, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		var row companyModel
		if err := db.Model(&companyModel{}).WhereEq("uuid", id).First(ctx.Request.Context(), &row); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, row)
	}
}

// companiesCreate handles POST /kiosks/companies. A company always has
// exactly one owner from the moment it exists — owner_email is required
// (createCompanyForm.Rules) and resolved the same way companiesAttachUser
// resolves any member: find the user by email, or create one.
func companiesCreate(db *playsql.DB, allowlist map[string]bool) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *createCompanyForm) {
		email := strings.ToLower(strings.TrimSpace(form.OwnerEmail))
		if allowlist[email] {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "developer accounts cannot own a company"})
			return
		}

		ownerRole, err := findRoleByName(ctx, db, "owner")
		if err != nil {
			ctx.Error(err)
			return
		}

		var row companyModel
		err = db.Model(&companyModel{}).Create(ctx.Request.Context(), &row, map[string]any{
			"uuid":       uuid.NewString(),
			"name":       form.Name,
			"status":     "active",
			"identifier": form.Identifier,
			"plan_id":    form.PlanID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}

		owner, err := findOrCreateCompanyUser(ctx, db, email, form.OwnerName)
		if err != nil {
			ctx.Error(err)
			return
		}

		var membership companyUserModel
		err = db.Model(&companyUserModel{}).Create(ctx.Request.Context(), &membership, map[string]any{
			"company_id": row.ID,
			"user_id":    owner.ID,
			"role_id":    ownerRole.ID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}

		ctx.JSON(http.StatusCreated, row)
	})
}

func companiesUpdateStatus(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *updateCompanyStatusForm) {
		id, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		if form.Status != "active" && form.Status != "suspended" {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "status must be active or suspended"})
			return
		}
		_, err := db.Model(&companyModel{}).WhereEq("uuid", id).Update(ctx.Request.Context(), map[string]any{
			"status": form.Status,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		companiesShow(db)(ctx)
	})
}

func companiesUpdatePlan(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *updateCompanyPlanForm) {
		id, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		_, err := db.Model(&companyModel{}).WhereEq("uuid", id).Update(ctx.Request.Context(), map[string]any{
			"plan_id": form.PlanID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		companiesShow(db)(ctx)
	})
}
