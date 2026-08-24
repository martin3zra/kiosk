package kiosk

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/foundation"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// companiesAttachUser handles POST /kiosks/companies/:id/users. It finds or
// creates the target user by email, then attaches them to the company —
// one combined action, not "create a user" followed by a separate "assign"
// step, so a company can never end up with a user that has no login.
//
// v1 assumes a user belongs to at most one company: company_users has no
// column marking a "current" membership (cactusHR's model does, for
// multi-company support), so attaching an already-assigned user to a
// *different* company is rejected rather than silently moving them.
// Re-attaching to the *same* company updates their role and is treated as
// idempotent.
func companiesAttachUser(db *playsql.DB, allowlist map[string]bool) routing.HandlerFunc {
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

		user, err := findOrCreateCompanyUser(ctx, db, email, form.Name)
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

// findOrCreateCompanyUser mirrors usersCreate's new-account shape (random
// temp password, must_change_password) for the case where email doesn't
// match an existing user yet.
func findOrCreateCompanyUser(ctx *routing.Context, db *playsql.DB, email, name string) (kioskUserModel, error) {
	var user kioskUserModel
	err := db.Model(&kioskUserModel{}).WhereEq("email", email).First(ctx.Request.Context(), &user)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, playsql.ErrNotFound) {
		return kioskUserModel{}, err
	}

	temp, err := randomPassword()
	if err != nil {
		return kioskUserModel{}, err
	}
	err = db.Model(&kioskUserModel{}).Create(ctx.Request.Context(), &user, map[string]any{
		"uuid":                 uuid.NewString(),
		"name":                 name,
		"email":                email,
		"role":                 "user",
		"password":             foundation.NewHashable().Make(temp),
		"must_change_password": true,
	})
	return user, err
}
