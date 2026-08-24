package kiosk

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/foundation"
	"github.com/martin3zra/forge/mailer"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/forge/session"
	"github.com/martin3zra/playsql"
)

const invitationTTL = 7 * 24 * time.Hour

// invitationMail is the "you've been invited" email — see
// mail/kiosk-invitation.html (copied by kiosk:install into the host's
// resources/views/mail/, alongside its other mail templates).
type invitationMail struct {
	invite      invitationModel
	companyName string
	acceptURL   string
}

func (m invitationMail) Subject() string {
	return fmt.Sprintf("You've been invited to join %s", m.companyName)
}
// To returns nil, not the invitee — sendInvitationEmail already sets them
// as the primary recipient via mailer.Mailer's own .To(...). Mailable.To()
// is for *additional* recipients (mailer.go's sendViaAPI/sendViaSMTP both
// append it to the primary), so returning the same person here would send
// the invitation to them twice under the API driver.
func (m invitationMail) To() []mailer.Individual { return nil }
// Content path is rooted the way forge itself roots template lookups (see
// forge/routing/context.go's "resources/views/error/500.html") — relative
// to the host's module root, not to any resources sub-package's own
// embed.FS root. spark's main.go embeds `resources/views/*` at that same
// module root and passes that embed.FS straight into mailer.New (see
// shop/app/server.go), so this must carry the "resources/" prefix to match.
func (m invitationMail) Content() string { return "resources/views/mail/kiosk-invitation.html" }
func (m invitationMail) Data() map[string]any {
	expires := ""
	if m.invite.ExpiresAt != nil {
		expires = m.invite.ExpiresAt.Format("January 2, 2006")
	}
	return map[string]any{
		"Subject": m.Subject(),
		"Title":   "You're invited",
		"Message": fmt.Sprintf(
			"You've been invited to join %s. Click below to accept and set up your account.",
			m.companyName,
		),
		"CTAText":     "Accept invitation",
		"CTAURL":      m.acceptURL,
		"ExpiresAt":   expires,
		"Year":        time.Now().Year(),
		"CompanyName": m.companyName,
	}
}
func (m invitationMail) Attachments() []mailer.Attachment { return nil }

// createOrRefreshInvitation finds-or-creates a pending invitation for
// (company, email) — a repeat invite to the same person for the same
// company regenerates the token/expiry on the existing row instead of
// piling up dead duplicates, matching what "resend" would do anyway.
func createOrRefreshInvitation(ctx *routing.Context, db *playsql.DB, companyID, roleID int, name, email string) (invitationModel, error) {
	token, err := randomToken()
	if err != nil {
		return invitationModel{}, err
	}
	expiresAt := time.Now().Add(invitationTTL)

	var existing invitationModel
	err = db.Model(&invitationModel{}).
		WhereEq("company_id", companyID).WhereEq("email", email).WhereNull("accepted_at").
		First(ctx.Request.Context(), &existing)
	switch {
	case err == nil:
		_, updateErr := db.Model(&invitationModel{}).WhereEq("id", existing.ID).Update(ctx.Request.Context(), map[string]any{
			"role_id":    roleID,
			"name":       name,
			"token":      token,
			"expires_at": expiresAt,
		})
		if updateErr != nil {
			return invitationModel{}, updateErr
		}
		existing.RoleID, existing.Name, existing.Token, existing.ExpiresAt = roleID, name, token, &expiresAt
		return existing, nil
	case !errors.Is(err, playsql.ErrNotFound):
		return invitationModel{}, err
	}

	var invite invitationModel
	err = db.Model(&invitationModel{}).Create(ctx.Request.Context(), &invite, map[string]any{
		"uuid":       uuid.NewString(),
		"company_id": companyID,
		"role_id":    roleID,
		"name":       name,
		"email":      email,
		"token":      token,
		"expires_at": expiresAt,
	})
	return invite, err
}

// sendInvitationEmail builds the accept URL and sends the email via
// whatever mailer.Mailer the host configured in Options.Mailer. mailer.Config
// is unexported from here, so Kiosk can't detect or default an unconfigured
// (zero-value) driver itself — a host that hasn't set MAIL_DRIVER should
// default it to mailer.Log when building that config, the same way it's
// responsible for KIOSK_DEVELOPERS/Options.Allowlist.
func sendInvitationEmail(m mailer.Mailer, invite invitationModel, companyName, appURL string) {
	acceptURL := strings.TrimRight(appURL, "/") + "/kiosks/invitations/" + invite.Token
	m.To(invite.Email, invite.Name).Send(invitationMail{invite: invite, companyName: companyName, acceptURL: acceptURL})
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// companiesInvitationsIndex handles GET /kiosks/companies/:id/invitations —
// pending (not accepted, not expired) invitations, for a "waiting to join"
// list alongside the members table.
func companiesInvitationsIndex(db *playsql.DB) routing.HandlerFunc {
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

		var invites []invitationModel
		if err := db.Model(&invitationModel{}).
			WhereEq("company_id", company.ID).WhereNull("accepted_at").
			OrderBy("created_at", playsql.Desc).
			Get(ctx.Request.Context(), &invites); err != nil {
			ctx.Error(err)
			return
		}

		type invitationRow struct {
			UUID      string `json:"uuid"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			ExpiresAt string `json:"expires_at"`
			Expired   bool   `json:"expired"`
		}
		rows := make([]invitationRow, 0, len(invites))
		for _, inv := range invites {
			var role roleModel
			if err := db.Model(&roleModel{}).Find(ctx.Request.Context(), &role, inv.RoleID); err != nil {
				ctx.Error(err)
				return
			}
			expiresAt := ""
			expired := true
			if inv.ExpiresAt != nil {
				expiresAt = inv.ExpiresAt.Format(time.RFC3339)
				expired = time.Now().After(*inv.ExpiresAt)
			}
			rows = append(rows, invitationRow{
				UUID: inv.UUID, Name: inv.Name, Email: inv.Email, Role: role.Name,
				ExpiresAt: expiresAt, Expired: expired,
			})
		}
		ctx.JSON(http.StatusOK, rows)
	}
}

// companiesInvitationsResend handles POST
// /kiosks/companies/:id/invitations/:invitationId/resend — regenerates the
// token/expiry and re-sends the email.
func companiesInvitationsResend(db *playsql.DB, m mailer.Mailer, appURL string) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		companyUUID, ok := paramUUID(ctx, "id")
		if !ok {
			return
		}
		invitationUUID, ok := paramUUID(ctx, "invitationId")
		if !ok {
			return
		}

		company, err := companiesShowRow(ctx, db, companyUUID)
		if err != nil {
			ctx.Error(err)
			return
		}

		var invite invitationModel
		err = db.Model(&invitationModel{}).
			WhereEq("uuid", invitationUUID).WhereEq("company_id", company.ID).WhereNull("accepted_at").
			First(ctx.Request.Context(), &invite)
		if err != nil {
			ctx.Error(err)
			return
		}

		invite, err = createOrRefreshInvitation(ctx, db, company.ID, invite.RoleID, invite.Name, invite.Email)
		if err != nil {
			ctx.Error(err)
			return
		}
		sendInvitationEmail(m, invite, company.Name, appURL)
		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}

// loadValidInvitation resolves a token to its invitation, company, and
// role rows — or an error if the token doesn't exist, was already
// accepted, or has expired. Used by both invitationsShow (render the
// accept page) and invitationsAccept (act on the submission).
func loadValidInvitation(ctx *routing.Context, db *playsql.DB, token string) (invitationModel, companyModel, roleModel, error) {
	var invite invitationModel
	if err := db.Model(&invitationModel{}).WhereEq("token", token).First(ctx.Request.Context(), &invite); err != nil {
		return invitationModel{}, companyModel{}, roleModel{}, err
	}
	if invite.AcceptedAt != nil {
		return invitationModel{}, companyModel{}, roleModel{}, errors.New("invitation already accepted")
	}
	if invite.ExpiresAt == nil || time.Now().After(*invite.ExpiresAt) {
		return invitationModel{}, companyModel{}, roleModel{}, errors.New("invitation expired")
	}

	var company companyModel
	if err := db.Model(&companyModel{}).Find(ctx.Request.Context(), &company, invite.CompanyID); err != nil {
		return invitationModel{}, companyModel{}, roleModel{}, err
	}
	var role roleModel
	if err := db.Model(&roleModel{}).Find(ctx.Request.Context(), &role, invite.RoleID); err != nil {
		return invitationModel{}, companyModel{}, roleModel{}, err
	}
	return invite, company, role, nil
}

// invitationsShow handles GET /kiosks/invitations/:token. Deliberately
// public — registered outside the allowlist-gated /kiosks group (see
// kiosk.go's Register) — the invited person has no account yet, so there's
// nothing to authenticate against.
func invitationsShow(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		token := ctx.Params["token"]
		invite, company, role, err := loadValidInvitation(ctx, db, token)
		if err != nil {
			ctx.Error(foundation.HTTPError{StatusCode: http.StatusNotFound, Message: "This invitation link is invalid or has expired."})
			return
		}
		ctx.Render("Kiosk/AcceptInvitation", map[string]any{
			"token":       invite.Token,
			"name":        invite.Name,
			"email":       invite.Email,
			"companyName": company.Name,
			"role":        role.Name,
		})
	}
}

// invitationsAccept handles POST /kiosks/invitations/:token — also public.
// Creates the real account with the password the invitee chose here (so,
// unlike an admin-created user, must_change_password is false from the
// start), attaches them to the company, marks the invitation accepted, and
// logs them in directly.
func invitationsAccept(db *playsql.DB) routing.HandlerFunc {
	return routing.WithRequest(func(ctx *routing.Context, form *acceptInvitationForm) {
		token := ctx.Params["token"]
		invite, _, _, err := loadValidInvitation(ctx, db, token)
		if err != nil {
			ctx.JSON(http.StatusNotFound, map[string]any{"error": "This invitation link is invalid or has expired."})
			return
		}
		if len(form.Password) < 8 {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "password must be at least 8 characters"})
			return
		}

		// A user with this email might already exist by the time this
		// invitation is accepted (e.g. they were invited to a second
		// company, or signed up separately) — attach the existing account
		// rather than erroring or creating a duplicate.
		var user kioskUserModel
		err = db.Model(&kioskUserModel{}).WhereEq("email", invite.Email).First(ctx.Request.Context(), &user)
		if errors.Is(err, playsql.ErrNotFound) {
			now := time.Now()
			err = db.Model(&kioskUserModel{}).Create(ctx.Request.Context(), &user, map[string]any{
				"uuid":                 uuid.NewString(),
				"name":                 invite.Name,
				"email":                invite.Email,
				"role":                 "user",
				"password":             foundation.NewHashable().Make(form.Password),
				"must_change_password": false,
				"email_verified_at":    now,
			})
		}
		if err != nil {
			ctx.Error(err)
			return
		}

		var existingMembership companyUserModel
		err = db.Model(&companyUserModel{}).WhereEq("user_id", user.ID).First(ctx.Request.Context(), &existingMembership)
		if errors.Is(err, playsql.ErrNotFound) {
			err = db.Model(&companyUserModel{}).Create(ctx.Request.Context(), &companyUserModel{}, map[string]any{
				"company_id": invite.CompanyID,
				"user_id":    user.ID,
				"role_id":    invite.RoleID,
			})
		}
		if err != nil {
			ctx.Error(err)
			return
		}

		acceptedAt := time.Now()
		if _, err := db.Model(&invitationModel{}).WhereEq("id", invite.ID).Update(ctx.Request.Context(), map[string]any{"accepted_at": acceptedAt}); err != nil {
			ctx.Error(err)
			return
		}

		// This is a fresh authentication event — establish it as one. The
		// request's session cookie could belong to anyone/anything (a
		// public link, opened in whatever browser state happened to be
		// sitting there), so clear any stale impersonation marker rather
		// than inheriting it: without this, a session that was mid
		// impersonation before landing here would keep showing the
		// "impersonating" banner using the just-accepted user as the
		// impersonation target. See ImpersonateSessionKey in impersonate.go.
		sess := session.GetSession(ctx.Request)
		sess.Delete(ImpersonateSessionKey)
		sess.Put("user_id", user.ID)
		sess.Put("user", sessionUserPayload(user))
		sess.Put("company_id", invite.CompanyID)

		ctx.JSON(http.StatusOK, map[string]any{"ok": true})
	})
}
