package kiosk

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/forge/session"
	"github.com/martin3zra/playsql"
)

func TestUsersCreateAndUpdate(t *testing.T) {
	db := testDB(t)

	status, user := doJSON(t, usersCreate(db), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("usersCreate: status=%d body=%v", status, user)
	}
	if user["Role"] != "user" {
		t.Fatalf("expected default role \"user\", got %v", user["Role"])
	}
	if user["MustChangePassword"] != true {
		t.Fatalf("expected must_change_password=true for a newly created user, got %v", user["MustChangePassword"])
	}
	if pw, _ := user["Password"].(string); pw == "" || pw == "grace@example.com" {
		t.Fatalf("expected a hashed random password, got %q", pw)
	}
	userUUID := user["UUID"].(string)

	status, _ = doJSON(t, usersCreate(db), http.MethodPost,
		`{"name":"Bad Role","email":"bad@example.com","role":"root"}`, nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an invalid role, got %d", status)
	}

	status, updated := doJSON(t, usersUpdate(db), http.MethodPut,
		`{"name":"Grace H","email":"grace@example.com","role":"admin"}`,
		map[string]string{"id": userUUID})
	if status != http.StatusOK || updated["Name"] != "Grace H" || updated["Role"] != "admin" {
		t.Fatalf("usersUpdate: status=%d body=%v", status, updated)
	}
}

// newTestSession builds a real *session.Session (via a DB-backed store, the
// same mechanism forge's own middleware uses to construct one) attached to a
// fresh request's context. session.Session's payload field is unexported, so
// there's no way to build a working one by hand from outside the session
// package — this goes through forge's own SessionManager.Start instead,
// which creates a fresh in-memory session without needing a request cookie.
func newTestSession(t *testing.T, db *playsql.DB) (*session.Session, *http.Request) {
	t.Helper()
	store := session.NewDatabaseStore(db)
	mgr := session.NewSessionManager(store, time.Hour, time.Hour, time.Hour, "test_session", "", false, false)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	sess, req := mgr.Start(req)
	return sess, req
}

func TestImpersonateLifecycle(t *testing.T) {
	db := testDB(t)
	ctx0 := t.Context()

	var developer kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(ctx0, &developer, map[string]any{
		"uuid": uuid.NewString(), "name": "Dev", "email": "dev@example.com",
		"password": "x", "role": "admin",
	}); err != nil {
		t.Fatalf("seed developer: %v", err)
	}

	var target kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(ctx0, &target, map[string]any{
		"uuid": uuid.NewString(), "name": "Target", "email": "target@example.com",
		"password": "x", "role": "user",
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	var otherDev kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(ctx0, &otherDev, map[string]any{
		"uuid": uuid.NewString(), "name": "Other Dev", "email": "other-dev@example.com",
		"password": "x", "role": "admin",
	}); err != nil {
		t.Fatalf("seed other developer: %v", err)
	}

	allowlist := map[string]bool{"dev@example.com": true, "other-dev@example.com": true}

	sess, req := newTestSession(t, db)
	sess.Put("user_id", developer.ID)
	sess.Put("user", sessionUserPayload(developer))

	// Can't impersonate another kiosk developer.
	rec := httptest.NewRecorder()
	rctx := &routing.Context{Response: rec, Request: req, Params: map[string]string{"id": otherDev.UUID}}
	impersonateStart(db, allowlist)(rctx)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 impersonating another developer, got %d", rec.Code)
	}

	// Start impersonating the target.
	rec = httptest.NewRecorder()
	rctx = &routing.Context{Response: rec, Request: req, Params: map[string]string{"id": target.UUID}}
	impersonateStart(db, allowlist)(rctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("impersonateStart: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := toInt(sess.Get("user_id")); !ok || got != target.ID {
		t.Fatalf("expected session user_id to become target's id, got %v", sess.Get("user_id"))
	}
	if got, ok := toInt(sess.Get(ImpersonateSessionKey)); !ok || got != developer.ID {
		t.Fatalf("expected original id stashed under %q, got %v", ImpersonateSessionKey, sess.Get(ImpersonateSessionKey))
	}

	// Can't stack a second impersonation.
	rec = httptest.NewRecorder()
	rctx = &routing.Context{Response: rec, Request: req, Params: map[string]string{"id": otherDev.UUID}}
	impersonateStart(db, allowlist)(rctx)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 stacking impersonation, got %d", rec.Code)
	}

	// A target with no company_users row leaves company_id unset.
	if sess.Get("company_id") != nil {
		t.Fatalf("expected no company_id for a target with no company membership, got %v", sess.Get("company_id"))
	}

	// An open audit row exists for this impersonation.
	var audit impersonationSessionModel
	if err := db.Model(&impersonationSessionModel{}).
		WhereEq("developer_user_id", developer.ID).WhereEq("target_user_id", target.ID).
		First(ctx0, &audit); err != nil {
		t.Fatalf("expected an impersonation_sessions row, got err=%v", err)
	}
	if audit.StartedAt == nil {
		t.Fatalf("expected started_at to be set")
	}
	if audit.EndedAt != nil {
		t.Fatalf("expected ended_at to be nil while impersonation is active, got %v", audit.EndedAt)
	}

	// Stop — restores the developer.
	rec = httptest.NewRecorder()
	rctx = &routing.Context{Response: rec, Request: req}
	impersonateStop(db)(rctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("impersonateStop: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := toInt(sess.Get("user_id")); !ok || got != developer.ID {
		t.Fatalf("expected session user_id restored to developer's id, got %v", sess.Get("user_id"))
	}
	if sess.Get(ImpersonateSessionKey) != nil {
		t.Fatalf("expected %q cleared after stop, still %v", ImpersonateSessionKey, sess.Get(ImpersonateSessionKey))
	}
	if sess.Get("company_id") != nil {
		t.Fatalf("expected company_id cleared after stop, still %v", sess.Get("company_id"))
	}

	if err := db.Model(&impersonationSessionModel{}).Find(ctx0, &audit, audit.ID); err != nil {
		t.Fatalf("reload audit row: %v", err)
	}
	if audit.EndedAt == nil {
		t.Fatalf("expected ended_at to be set after impersonateStop")
	}

	// Stopping again with nothing to stop is a client error, not a panic.
	rec = httptest.NewRecorder()
	rctx = &routing.Context{Response: rec, Request: req}
	impersonateStop(db)(rctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 stopping with no active impersonation, got %d", rec.Code)
	}
}

// TestImpersonateSetsCompanyIDFromMembership covers the other half of the
// company_id behavior TestImpersonateLifecycle doesn't: a target who *does*
// belong to a company. This is the signal a host app's own developer-must-
// impersonate-to-see-company-pages guard reads.
func TestImpersonateSetsCompanyIDFromMembership(t *testing.T) {
	db := testDB(t)
	ctx0 := t.Context()

	var developer kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(ctx0, &developer, map[string]any{
		"uuid": uuid.NewString(), "name": "Dev", "email": "dev@example.com",
		"password": "x", "role": "admin",
	}); err != nil {
		t.Fatalf("seed developer: %v", err)
	}

	var target kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(ctx0, &target, map[string]any{
		"uuid": uuid.NewString(), "name": "Target", "email": "target@example.com",
		"password": "x", "role": "user",
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	var company companyModel
	if err := db.Model(&companyModel{}).Create(ctx0, &company, map[string]any{
		"uuid": uuid.NewString(), "name": "Acme", "status": "active",
	}); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	if err := db.Model(&companyUserModel{}).Create(ctx0, &companyUserModel{}, map[string]any{
		"company_id": company.ID, "user_id": target.ID, "role": "member",
	}); err != nil {
		t.Fatalf("seed company_users row: %v", err)
	}

	allowlist := map[string]bool{"dev@example.com": true}
	sess, req := newTestSession(t, db)
	sess.Put("user_id", developer.ID)
	sess.Put("user", sessionUserPayload(developer))

	rec := httptest.NewRecorder()
	rctx := &routing.Context{Response: rec, Request: req, Params: map[string]string{"id": target.UUID}}
	impersonateStart(db, allowlist)(rctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("impersonateStart: status=%d body=%s", rec.Code, rec.Body.String())
	}

	if got, ok := toInt(sess.Get("company_id")); !ok || got != company.ID {
		t.Fatalf("expected session company_id=%d, got %v", company.ID, sess.Get("company_id"))
	}
}
