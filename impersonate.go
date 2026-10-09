package kiosk

import (
	"net/http"
	"time"

	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/forge/session"
	"github.com/martin3zra/playsql"
)

// ImpersonateSessionKey holds the original developer's user id in session
// while impersonating someone — its presence is the signal that
// impersonation is active. Exported so a host app can read it directly
// (e.g. to show a "stop impersonating" banner) without duplicating the
// string or depending on company_id, which answers a different question
// (does the *current* identity have a company) than "is this session
// currently impersonating."
const ImpersonateSessionKey = "kiosk_original_user_id"

// impersonateStart handles POST /kiosks/impersonate/:id. Besides swapping
// the session identity, it opens an impersonation_sessions audit row and
// seeds session["company_id"] from the target's company_users membership —
// the signal a host app's own "developer must impersonate to see company
// pages" guard reads (see RedirectDeveloperToKiosk-style middleware in the
// host app; Kiosk itself has no opinion on what a developer without a
// company_id is allowed to view).
func impersonateStart(db *playsql.DB, allowlist map[string]bool) routing.HandlerFunc {
	return impersonateStartWith(db, developerCheck{allowlist: allowlist}, true)
}

// impersonateStartWith is impersonateStart with the full developer check
// (allowlist plus Tenancy.IsDeveloper) for the target guard. seedCompany
// false (Host mode) leaves company_id unset: the host picks the tenant.
func impersonateStartWith(db *playsql.DB, dev developerCheck, seedCompany bool) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		targetUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}

		// Checked before even looking up the target: stacking a second
		// impersonation is rejected regardless of who it's aimed at.
		sess := session.GetSession(ctx.Request)
		if sess.Get(ImpersonateSessionKey) != nil {
			ctx.JSON(http.StatusConflict, map[string]any{"error": "already impersonating — stop first"})
			return
		}

		actorID, ok := toInt(sess.Get("user_id"))
		if !ok {
			ctx.JSON(http.StatusUnauthorized, map[string]any{"error": "not authenticated"})
			return
		}

		var target kioskUserModel
		if err := db.Model(&kioskUserModel{}).WhereEq("uuid", targetUUID).First(ctx.Request.Context(), &target); err != nil {
			ctx.Error(err)
			return
		}

		if dev.target(target) {
			ctx.JSON(http.StatusForbidden, map[string]any{"error": "cannot impersonate another kiosk developer"})
			return
		}

		ip := ctx.Request.RemoteAddr
		ua := ctx.Request.UserAgent()
		var auditRow impersonationSessionModel
		err := db.Model(&impersonationSessionModel{}).Create(ctx.Request.Context(), &auditRow, map[string]any{
			"developer_user_id": actorID,
			"target_user_id":    target.ID,
			"started_at":        time.Now(),
			"ip_address":        ip,
			"user_agent":        ua,
		})
		if err != nil {
			ctx.Error(err)
			return
		}

		// A target with no company_users row yet (freshly created, not
		// assigned) leaves company_id unset — same as a developer who never
		// impersonated. Not an error: assigning them to a company is a
		// separate step (see companiesAttachUser).
		var membership companyUserModel
		companyErr := playsql.ErrNotFound
		if seedCompany {
			companyErr = db.Model(&companyUserModel{}).WhereEq("user_id", target.ID).First(ctx.Request.Context(), &membership)
		}

		sess.Put(ImpersonateSessionKey, actorID)
		sess.Put("user_id", target.ID)
		sess.Put("user", sessionUserPayload(target))
		if companyErr == nil {
			sess.Put("company_id", membership.CompanyID)
		} else {
			sess.Delete("company_id")
		}

		ctx.JSON(http.StatusOK, map[string]any{"ok": true, "impersonating": target.Email})
	}
}

// impersonateStop handles DELETE /kiosks/impersonate. Closes the audit row
// impersonateStart opened and drops company_id — the developer goes back to
// having no company context, same as before they impersonated anyone.
func impersonateStop(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		sess := session.GetSession(ctx.Request)

		originalID, ok := toInt(sess.Get(ImpersonateSessionKey))
		if !ok {
			ctx.JSON(http.StatusBadRequest, map[string]any{"error": "not impersonating"})
			return
		}

		var original kioskUserModel
		if err := db.Model(&kioskUserModel{}).Find(ctx.Request.Context(), &original, originalID); err != nil {
			ctx.Error(err)
			return
		}

		_, err := db.Model(&impersonationSessionModel{}).
			WhereEq("developer_user_id", originalID).
			WhereNull("ended_at").
			Update(ctx.Request.Context(), map[string]any{"ended_at": time.Now()})
		if err != nil {
			ctx.Error(err)
			return
		}

		sess.Put("user_id", original.ID)
		sess.Put("user", sessionUserPayload(original))
		sess.Delete(ImpersonateSessionKey)
		sess.Delete("company_id")

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
