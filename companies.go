package kiosk

import (
	"errors"
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
// exactly one owner designated from the moment it exists — owner_email is
// required (createCompanyForm.Rules). If that email matches an existing
// user they're attached immediately (role "owner"); otherwise, matching
// companiesAttachUser, an invitation is sent rather than eagerly creating
// an account nobody's confirmed yet — the company itself is still created
// either way, just without a company_users row until the invite is accepted.
//
// The company row insert and the CompanyCreated event dispatch run together
// in one transaction: a host that provisions per-company defaults from a
// listener never ends up with a company that's missing them. The owner
// attach/invite step stays outside that transaction — it already tolerates
// partial success (a failed invitation email doesn't undo the company).
func companiesCreate(opts Options) routing.HandlerFunc {
	db, m, allowlist, appURL := opts.DB, opts.Mailer, opts.Allowlist, opts.AppURL

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
		err = db.Tx(ctx.Request.Context(), func(tx *playsql.Tx) error {
			if txErr := tx.Model(&companyModel{}).Create(ctx.Request.Context(), &row, map[string]any{
				"uuid":       uuid.NewString(),
				"name":       form.Name,
				"status":     "active",
				"identifier": form.Identifier,
				"plan_id":    form.PlanID,
			}); txErr != nil {
				return txErr
			}
			return opts.Events.Dispatch(ctx.Request.Context(), tx, CompanyCreated{
				CompanyID: row.ID,
				Name:      row.Name,
			})
		})
		if err != nil {
			ctx.Error(err)
			return
		}

		var owner kioskUserModel
		err = db.Model(&kioskUserModel{}).WhereEq("email", email).First(ctx.Request.Context(), &owner)
		switch {
		case err == nil:
			err = db.Model(&companyUserModel{}).Create(ctx.Request.Context(), &companyUserModel{}, map[string]any{
				"company_id": row.ID,
				"user_id":    owner.ID,
				"role_id":    ownerRole.ID,
			})
			if err != nil {
				ctx.Error(err)
				return
			}
			ctx.JSON(http.StatusCreated, map[string]any{"company": row, "owner_invited": false})
		case errors.Is(err, playsql.ErrNotFound):
			invite, inviteErr := createOrRefreshInvitation(ctx, db, row.ID, ownerRole.ID, form.OwnerName, email)
			if inviteErr != nil {
				ctx.Error(inviteErr)
				return
			}
			sendInvitationEmail(m, invite, row.Name, appURL)
			ctx.JSON(http.StatusCreated, map[string]any{"company": row, "owner_invited": true})
		default:
			ctx.Error(err)
		}
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
