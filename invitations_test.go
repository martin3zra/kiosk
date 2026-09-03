package kiosk

import (
	"embed"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/martin3zra/forge/mailer"
	"github.com/martin3zra/forge/routing"
)

// testViewsFS embeds resources/views/mail/kiosk-invitation.html (a
// test-only fixture at the exact path invitationMail.Content() returns —
// see that file's comment for why it's not just mail/kiosk-invitation.html,
// the real install source).
//
//go:embed resources
var testViewsFS embed.FS

func testMailer(t *testing.T) (mailer.Mailer, *strings.Builder) {
	t.Helper()
	var out strings.Builder
	log.SetOutput(&out)
	// Restore the real default (os.Stderr), not nil — log.SetOutput(nil)
	// would leave the *package-global* logger broken for every test that
	// runs after this one, not just this test.
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	cfg := mailer.Config{Driver: mailer.Log, FromAddress: "noreply@example.com", FromName: "Example"}
	return mailer.New(cfg, testViewsFS), &out
}

func TestCreateOrRefreshInvitation(t *testing.T) {
	db := testDB(t)
	ctx := &routing.Context{Request: httptest.NewRequest(http.MethodPost, "/", nil)}

	var role roleModel
	if err := db.Model(&roleModel{}).WhereEq("name", "admin").First(t.Context(), &role); err != nil {
		t.Fatalf("seed role lookup: %v", err)
	}

	first, err := createOrRefreshInvitation(ctx.Request.Context(), db, 1, role.ID, "Grace", "grace@example.com")
	if err != nil {
		t.Fatalf("createOrRefreshInvitation: %v", err)
	}
	if first.Token == "" || first.ExpiresAt == nil {
		t.Fatalf("expected a token and expiry, got %+v", first)
	}

	count, err := db.Model(&invitationModel{}).WhereEq("email", "grace@example.com").Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected exactly 1 invitation row, got count=%d err=%v", count, err)
	}

	// A second invite to the same (company, email) refreshes the existing
	// row — new token, same row — rather than creating a duplicate.
	second, err := createOrRefreshInvitation(ctx.Request.Context(), db, 1, role.ID, "Grace", "grace@example.com")
	if err != nil {
		t.Fatalf("createOrRefreshInvitation (refresh): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected the same invitation row to be refreshed, got a different id")
	}
	if second.Token == first.Token {
		t.Fatalf("expected a fresh token on refresh")
	}

	count, err = db.Model(&invitationModel{}).WhereEq("email", "grace@example.com").Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected still exactly 1 invitation row after refresh, got count=%d err=%v", count, err)
	}
}

func TestCompaniesAttachUserInvitesUnknownEmail(t *testing.T) {
	db := testDB(t)
	m, log := testMailer(t)
	allowlist := map[string]bool{}

	status, company := doJSON(t, companiesCreate(Options{DB: db, Mailer: m, Allowlist: allowlist, AppURL: "https://app.example.com"}), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Owner","owner_email":"owner@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, company)
	}
	companyUUID := company["company"].(map[string]any)["UUID"].(string)

	status, body := doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, map[string]string{"id": companyUUID})
	if status != http.StatusAccepted {
		t.Fatalf("expected 202 inviting an unknown email, got %d body=%v", status, body)
	}
	if body["invited"] != true {
		t.Fatalf("expected invited=true, got %v", body)
	}

	// No user or company_users row should exist yet — only the invitation.
	count, err := db.Model(&kioskUserModel{}).WhereEq("email", "grace@example.com").Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("expected no user created for an invited email, got count=%d err=%v", count, err)
	}
	count, err = db.Model(&invitationModel{}).WhereEq("email", "grace@example.com").Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected exactly 1 invitation row, got count=%d err=%v", count, err)
	}

	if !strings.Contains(log.String(), "grace@example.com") {
		t.Fatalf("expected the log driver to have recorded the invitation email, got:\n%s", log.String())
	}
}

func TestInvitationAcceptCreatesAccountAndLogsIn(t *testing.T) {
	db := testDB(t)
	m, _ := testMailer(t)
	allowlist := map[string]bool{}

	status, company := doJSON(t, companiesCreate(Options{DB: db, Mailer: m, Allowlist: allowlist, AppURL: "https://app.example.com"}), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Owner","owner_email":"owner@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, company)
	}
	companyUUID := company["company"].(map[string]any)["UUID"].(string)

	status, _ = doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com","role":"admin"}`, map[string]string{"id": companyUUID})
	if status != http.StatusAccepted {
		t.Fatalf("expected 202 inviting Grace, got %d", status)
	}

	var invite invitationModel
	if err := db.Model(&invitationModel{}).WhereEq("email", "grace@example.com").First(t.Context(), &invite); err != nil {
		t.Fatalf("expected an invitation row: %v", err)
	}

	sess, sessReq := newTestSession(t, db)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"password":"a-real-password"}`)).WithContext(sessReq.Context())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	ctx := &routing.Context{Response: rec, Request: req, Params: map[string]string{"token": invite.Token}}

	invitationsAccept(db)(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitationsAccept: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var user kioskUserModel
	if err := db.Model(&kioskUserModel{}).WhereEq("email", "grace@example.com").First(t.Context(), &user); err != nil {
		t.Fatalf("expected the invited user to now exist: %v", err)
	}
	if user.MustChangePassword {
		t.Fatalf("expected must_change_password=false — the invitee set their own password")
	}

	count, err := db.Model(&companyUserModel{}).WhereEq("user_id", user.ID).Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected exactly 1 company_users row after accept, got count=%d err=%v", count, err)
	}

	if err := db.Model(&invitationModel{}).Find(t.Context(), &invite, invite.ID); err != nil {
		t.Fatalf("reload invitation: %v", err)
	}
	if invite.AcceptedAt == nil {
		t.Fatalf("expected accepted_at to be set")
	}

	if got, ok := toInt(sess.Get("user_id")); !ok || got != user.ID {
		t.Fatalf("expected the session to be logged in as the new user, got %v", sess.Get("user_id"))
	}
	if sess.Get("company_id") == nil {
		t.Fatalf("expected company_id to be set in session after accepting")
	}
}

// TestInvitationAcceptClearsStaleImpersonationMarker guards against a real
// bug found via live testing: the request's session cookie can belong to
// whatever browser state happened to hit this public link — including a
// session that was mid-impersonation. Without explicitly clearing
// ImpersonateSessionKey, invitationsAccept would leave it in place, and a
// host's "impersonating" banner would then read it as still active,
// pointing at the just-accepted user as if they were the impersonation
// target.
func TestInvitationAcceptClearsStaleImpersonationMarker(t *testing.T) {
	db := testDB(t)
	m, _ := testMailer(t)
	allowlist := map[string]bool{}

	status, company := doJSON(t, companiesCreate(Options{DB: db, Mailer: m, Allowlist: allowlist, AppURL: "https://app.example.com"}), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Owner","owner_email":"owner@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, company)
	}
	companyUUID := company["company"].(map[string]any)["UUID"].(string)

	status, _ = doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com","role":"admin"}`, map[string]string{"id": companyUUID})
	if status != http.StatusAccepted {
		t.Fatalf("expected 202 inviting Grace, got %d", status)
	}

	var invite invitationModel
	if err := db.Model(&invitationModel{}).WhereEq("email", "grace@example.com").First(t.Context(), &invite); err != nil {
		t.Fatalf("expected an invitation row: %v", err)
	}

	sess, sessReq := newTestSession(t, db)
	sess.Put(ImpersonateSessionKey, 999) // stale marker, as if left over from an unrelated impersonation session
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"password":"a-real-password"}`)).WithContext(sessReq.Context())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	ctx := &routing.Context{Response: rec, Request: req, Params: map[string]string{"token": invite.Token}}

	invitationsAccept(db)(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitationsAccept: status=%d body=%s", rec.Code, rec.Body.String())
	}

	if sess.Get(ImpersonateSessionKey) != nil {
		t.Fatalf("expected the stale impersonation marker to be cleared, got %v", sess.Get(ImpersonateSessionKey))
	}
}

// TestLoadValidInvitationRejectsExpiredAndAccepted tests loadValidInvitation
// directly rather than through invitationsShow's HTTP handler: on a lookup
// failure it calls ctx.Error(foundation.HTTPError{...}), whose non-production
// branch renders an HTML template from ctx.resources (routing.Context's
// unexported embed.FS field) — real host apps have a real one; a bare test
// Context doesn't, and there's no exported way to set it from here. Verified
// live against a real server instead (curl) — same reasoning already applies
// to allowlistMiddleware's identical ctx.Error(...) pattern, untested at the
// HTTP layer here for the same reason.
func TestLoadValidInvitationRejectsExpiredAndAccepted(t *testing.T) {
	db := testDB(t)
	ctx0 := t.Context()
	ctx := &routing.Context{Request: httptest.NewRequest(http.MethodGet, "/", nil)}

	var role roleModel
	if err := db.Model(&roleModel{}).WhereEq("name", "admin").First(ctx0, &role); err != nil {
		t.Fatalf("seed role lookup: %v", err)
	}
	var company companyModel
	if err := db.Model(&companyModel{}).Create(ctx0, &company, map[string]any{
		"uuid": "c-uuid", "name": "Acme", "status": "active",
	}); err != nil {
		t.Fatalf("seed company: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	var expired invitationModel
	if err := db.Model(&invitationModel{}).Create(ctx0, &expired, map[string]any{
		"uuid": "expired-uuid", "company_id": company.ID, "role_id": role.ID,
		"name": "Grace", "email": "grace@example.com", "token": "expired-token",
		"expires_at": past,
	}); err != nil {
		t.Fatalf("seed expired invitation: %v", err)
	}
	if _, _, _, err := loadValidInvitation(ctx, db, "expired-token"); err == nil {
		t.Fatalf("expected an error loading an expired invitation")
	}

	future := time.Now().Add(time.Hour)
	acceptedAt := time.Now()
	var accepted invitationModel
	if err := db.Model(&invitationModel{}).Create(ctx0, &accepted, map[string]any{
		"uuid": "accepted-uuid", "company_id": company.ID, "role_id": role.ID,
		"name": "Hank", "email": "hank@example.com", "token": "accepted-token",
		"expires_at": future, "accepted_at": acceptedAt,
	}); err != nil {
		t.Fatalf("seed accepted invitation: %v", err)
	}
	if _, _, _, err := loadValidInvitation(ctx, db, "accepted-token"); err == nil {
		t.Fatalf("expected an error loading an already-accepted invitation")
	}

	if _, _, _, err := loadValidInvitation(ctx, db, "no-such-token"); err == nil {
		t.Fatalf("expected an error loading a nonexistent token")
	}
}
