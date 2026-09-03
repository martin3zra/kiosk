package kiosk

import (
	"errors"
	"net/http"
	"strings"

	"github.com/martin3zra/forge/mailer"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// companiesAttachUser handles POST /kiosks/companies/:id/users. If Email
// matches an existing user they're attached immediately — one combined
// action, not "create a user" followed by a separate "assign" step, so a
// company can never end up with a member that has no login. If it doesn't,
// an invitation is sent instead of eagerly creating an account nobody's
// confirmed yet (see createOrRefreshInvitation in invitations.go); the
// company_users row is only created once that invitation is accepted.
//
// v1 assumes a user belongs to at most one company: company_users has no
// column marking a "current" membership (cactusHR's model does, for
// multi-company support), so attaching an already-assigned user to a
// *different* company is rejected rather than silently moving them.
// Re-attaching to the *same* company updates their role and is treated as
// idempotent.
func companiesAttachUser(db *playsql.DB, m mailer.Mailer, allowlist map[string]bool, appURL string) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *assignCompanyUserForm) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}

		email := strings.ToLower(strings.TrimSpace(form.Email))
		if allowlist[email] {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "developer accounts cannot be company members"})
			return
		}

		roleName := form.Role
		if roleName == "" {
			roleName = "admin"
		}
		role, err := findRoleByName(ctx, db, roleName)
		if err != nil {
			if errors.Is(err, playsql.ErrNotFound) {
				ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "unknown role: " + roleName})
				return
			}
			ctx.Error(err)
			return
		}

		company, err := companiesShowRow(ctx, db, companyUUID)
		if err != nil {
			ctx.Error(err)
			return
		}

		var user kioskUserModel
		err = db.Model(&kioskUserModel{}).WhereEq("email", email).First(ctx.Request.Context(), &user)
		if errors.Is(err, playsql.ErrNotFound) {
			invite, inviteErr := createOrRefreshInvitation(ctx.Request.Context(), db, company.ID, role.ID, form.Name, email)
			if inviteErr != nil {
				ctx.Error(inviteErr)
				return
			}
			sendInvitationEmail(m, invite, company.Name, appURL)
			ctx.JSON(http.StatusAccepted, map[string]any{"invited": true, "email": email})
			return
		}
		if err != nil {
			ctx.Error(err)
			return
		}

		var existing companyUserModel
		err = db.Model(&companyUserModel{}).WhereEq("user_id", user.ID).First(ctx.Request.Context(), &existing)
		switch {
		case err == nil && existing.CompanyID == company.ID:
			_, updateErr := db.Model(&companyUserModel{}).WhereEq("id", existing.ID).Update(ctx.Request.Context(), map[string]any{"role_id": role.ID})
			if updateErr != nil {
				ctx.Error(updateErr)
				return
			}
			ctx.JSON(http.StatusOK, memberRow{UUID: user.UUID, Name: user.Name, Email: user.Email, Role: role.Name})
			return
		case err == nil:
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "user already belongs to a different company"})
			return
		case !errors.Is(err, playsql.ErrNotFound):
			ctx.Error(err)
			return
		}

		var membership companyUserModel
		err = db.Model(&companyUserModel{}).Create(ctx.Request.Context(), &membership, map[string]any{
			"company_id": company.ID,
			"user_id":    user.ID,
			"role_id":    role.ID,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, memberRow{UUID: user.UUID, Name: user.Name, Email: user.Email, Role: role.Name})
	})
}

// memberRow is a company_users row joined with its user and role names —
// what companiesAttachUser and companiesUsersIndex both return, since the
// frontend needs a display name for the role, not its internal id.
type memberRow struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// companiesUsersIndex handles GET /kiosks/companies/:id/users. Each row's
// "uuid" is the member's own user uuid — what a companiesDetachUser link
// needs, not their (never URL-facing) internal id.
func companiesUsersIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		company, err := companiesShowRow(ctx, db, companyUUID)
		if err != nil {
			ctx.Error(err)
			return
		}

		var memberships []companyUserModel
		if err := db.Model(&companyUserModel{}).WhereEq("company_id", company.ID).Get(ctx.Request.Context(), &memberships); err != nil {
			ctx.Error(err)
			return
		}

		rows := make([]memberRow, 0, len(memberships))
		for _, m := range memberships {
			var u kioskUserModel
			if err := db.Model(&kioskUserModel{}).Find(ctx.Request.Context(), &u, m.UserID); err != nil {
				ctx.Error(err)
				return
			}
			roleName := ""
			if m.RoleID != nil {
				var role roleModel
				if err := db.Model(&roleModel{}).Find(ctx.Request.Context(), &role, *m.RoleID); err != nil {
					ctx.Error(err)
					return
				}
				roleName = role.Name
			}
			rows = append(rows, memberRow{UUID: u.UUID, Name: u.Name, Email: u.Email, Role: roleName})
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

// companiesDetachUser handles DELETE /kiosks/companies/:id/users/:userId —
// both uuids, resolved to internal ids before matching them against
// company_users' integer FK columns.
func companiesDetachUser(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		userUUID, ok := paramUUID(ctx, "userId")
		if !ok {
			return
		}

		company, err := companiesShowRow(ctx, db, companyUUID)
		if err != nil {
			ctx.Error(err)
			return
		}
		var user kioskUserModel
		if err := db.Model(&kioskUserModel{}).WhereEq("uuid", userUUID).First(ctx.Request.Context(), &user); err != nil {
			ctx.Error(err)
			return
		}

		if _, err := db.Model(&companyUserModel{}).WhereEq("company_id", company.ID).WhereEq("user_id", user.ID).ForceDelete(ctx.Request.Context()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}

func companiesShowRow(ctx *routing.Context, db *playsql.DB, uuid string) (companyModel, error) {
	var row companyModel
	err := db.Model(&companyModel{}).WhereEq("uuid", uuid).First(ctx.Request.Context(), &row)
	return row, err
}
