package kiosk

import (
	"net/http"
	"strings"

	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/forge/session"
	"github.com/martin3zra/playsql"
)

const impersonateSessionKey = "kiosk_original_user_id"

// impersonateStart handles POST /kiosks/impersonate/:id. Session-only for
// v1 — no audit table — per the technical spec.
func impersonateStart(db *playsql.DB, allowlist map[string]bool) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		targetID, ok := paramInt(ctx, "id")
		if !ok {
			return
		}

		// Checked before even looking up the target: stacking a second
		// impersonation is rejected regardless of who it's aimed at.
		sess := session.GetSession(ctx.Request)
		if sess.Get(impersonateSessionKey) != nil {
			ctx.JSON(http.StatusConflict, map[string]any{"error": "already impersonating — stop first"})
			return
		}

		var target kioskUserModel
		if err := db.Model(&kioskUserModel{}).Find(ctx.Request.Context(), &target, targetID); err != nil {
			ctx.Error(err)
			return
		}

		if allowlist[strings.ToLower(target.Email)] {
			ctx.JSON(http.StatusForbidden, map[string]any{"error": "cannot impersonate another kiosk developer"})
			return
		}

		sess.Put(impersonateSessionKey, sess.Get("user_id"))
		sess.Put("user_id", target.ID)
		sess.Put("user", sessionUserPayload(target))

		ctx.JSON(http.StatusOK, map[string]any{"ok": true, "impersonating": target.Email})
	}
}

// impersonateStop handles DELETE /kiosks/impersonate.
func impersonateStop(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		sess := session.GetSession(ctx.Request)

		originalID, ok := toInt(sess.Get(impersonateSessionKey))
		if !ok {
			ctx.JSON(http.StatusBadRequest, map[string]any{"error": "not impersonating"})
			return
		}

		var original kioskUserModel
		if err := db.Model(&kioskUserModel{}).Find(ctx.Request.Context(), &original, originalID); err != nil {
			ctx.Error(err)
			return
		}

		sess.Put("user_id", original.ID)
		sess.Put("user", sessionUserPayload(original))
		sess.Delete(impersonateSessionKey)

		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}

// sessionUserPayload builds the same shape ReGenerate stores on login
// (app/auth-handlers.go via forge's SessionManager.ReGenerate), matching
// spark's AuthUser JSON tags field-for-field — see app/auth-user.go. This
// is what AuthUserFromContext expects to find as sess.Get("user") on every
// later request.
func sessionUserPayload(u kioskUserModel) map[string]any {
	return map[string]any{
		"id":                   u.ID,
		"uuid":                 u.UUID,
		"name":                 u.Name,
		"email":                u.Email,
		"role":                 u.Role,
		"email_verified_at":    u.EmailVerifiedAt,
		"must_change_password": u.MustChangePassword,
		"created_at":           u.CreatedAt,
		"updated_at":           u.UpdatedAt,
		"deleted_at":           u.DeletedAt,
	}
}

// toInt accepts the two shapes an id round-tripped through the session
// store can arrive in: the native int it was Put with with in this same
// request, or the float64 encoding/json produces once the session payload
// has been serialized to the sessions table and reloaded.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
