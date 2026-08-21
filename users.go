package kiosk

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/foundation"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// kioskUserModel maps Kiosk's own queries onto spark's users table — the one
// table Kiosk didn't create itself. It mirrors app.userModel field-for-field
// (see app/auth-user.go); Kiosk can't import that unexported type, and
// depending on it would point the module dependency the wrong way anyway.
type kioskUserModel struct {
	ID                 int        `db:"id"`
	UUID               string     `db:"uuid"`
	Name               string     `db:"name"`
	Email              string     `db:"email"`
	Password           string     `db:"password"`
	Role               string     `db:"role"`
	EmailVerifiedAt    *time.Time `db:"email_verified_at"`
	MustChangePassword bool       `db:"must_change_password"`
	CreatedAt          *time.Time `db:"created_at"`
	UpdatedAt          *time.Time `db:"updated_at"`
	DeletedAt          *time.Time `db:"deleted_at"`
}

func (kioskUserModel) TableName() string { return "users" }

func usersIndex(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		var rows []kioskUserModel
		if err := db.Model(&kioskUserModel{}).OrderBy("name", playsql.Asc).Get(ctx.Request.Context(), &rows); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

func usersCreate(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *createUserForm) {
		role := form.Role
		if role == "" {
			role = "user"
		}
		if role != "admin" && role != "user" {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "role must be admin or user"})
			return
		}

		temp, err := randomPassword()
		if err != nil {
			ctx.Error(err)
			return
		}

		var row kioskUserModel
		err = db.Model(&kioskUserModel{}).Create(ctx.Request.Context(), &row, map[string]any{
			"uuid":                 uuid.NewString(),
			"name":                 form.Name,
			"email":                form.Email,
			"role":                 role,
			"password":             foundation.NewHashable().Make(temp),
			"must_change_password": true,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, row)
	})
}

func usersUpdate(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *updateUserForm) {
		id, ok := paramInt(ctx, "id")
		if !ok {
			return
		}
		if form.Role != "admin" && form.Role != "user" {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "role must be admin or user"})
			return
		}

		_, err := db.Model(&kioskUserModel{}).WhereEq("id", id).Update(ctx.Request.Context(), map[string]any{
			"name":  form.Name,
			"email": form.Email,
			"role":  form.Role,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		var row kioskUserModel
		if err := db.Model(&kioskUserModel{}).Find(ctx.Request.Context(), &row, id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, row)
	})
}

// randomPassword generates a one-off password for a newly created user, who
// is created with must_change_password so this value is never meant to be
// communicated or used to log in directly — there's no invite/email flow
// yet (spark has no mailer wiring for it), so an admin resetting the
// password out of band is the v1 story.
func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
