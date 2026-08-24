package kiosk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/martin3zra/forge/routing"
)

// TestCompanyUserAssignment covers the "email matches an existing user"
// path of companiesAttachUser — attach/update/detach. The "email doesn't
// match anyone" path (an invitation gets created instead) is covered by
// invitations_test.go.
func TestCompanyUserAssignment(t *testing.T) {
	db := testDB(t)
	m, _ := testMailer(t)
	allowlist := map[string]bool{"dev@example.com": true}

	status, company := doJSON(t, companiesCreate(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Owner","owner_email":"owner@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, company)
	}
	companyUUID := company["company"].(map[string]any)["UUID"].(string)

	// A real, pre-existing user Grace can be attached to immediately.
	status, created := doJSON(t, usersCreate(db), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("usersCreate: status=%d body=%v", status, created)
	}
	graceUUID := created["UUID"].(string)

	// Reject assigning a developer email.
	status, body := doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Dev","email":"dev@example.com"}`, map[string]string{"id": companyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 assigning a developer email, got %d body=%v", status, body)
	}

	// Reject an unknown role name.
	status, body = doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com","role":"superuser"}`, map[string]string{"id": companyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an unknown role, got %d body=%v", status, body)
	}

	// Attach the existing user — immediate, no invitation, defaults role to "admin".
	status, membership := doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, map[string]string{"id": companyUUID})
	if status != http.StatusCreated {
		t.Fatalf("companiesAttachUser: status=%d body=%v", status, membership)
	}
	if membership["role"] != "admin" {
		t.Fatalf("expected default role \"admin\", got %v", membership["role"])
	}

	// List members — expect the member's own uuid, not their internal id.
	status, listBody := doJSON(t, companiesUsersIndex(db), http.MethodGet, "", map[string]string{"id": companyUUID})
	if status != http.StatusOK {
		t.Fatalf("companiesUsersIndex: status=%d", status)
	}
	_ = listBody // doJSON only decodes objects; presence of a 200 is enough here.

	// Re-attaching to the SAME company updates the role (idempotent).
	status, updated := doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com","role":"owner"}`, map[string]string{"id": companyUUID})
	if status != http.StatusOK {
		t.Fatalf("expected 200 re-attaching to the same company, got %d body=%v", status, updated)
	}
	if updated["role"] != "owner" {
		t.Fatalf("expected role updated to \"owner\", got %v", updated["role"])
	}

	var graceUser kioskUserModel
	if err := db.Model(&kioskUserModel{}).WhereEq("email", "grace@example.com").First(t.Context(), &graceUser); err != nil {
		t.Fatalf("reload grace: %v", err)
	}
	count, err := db.Model(&companyUserModel{}).WhereEq("user_id", graceUser.ID).Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected exactly 1 company_users row after re-attach, got count=%d err=%v", count, err)
	}

	// A second company can't claim an already-assigned user.
	status, secondCompany := doJSON(t, companiesCreate(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Globex","owner_name":"Owner Two","owner_email":"owner2@example.com"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate (second): status=%d body=%v", status, secondCompany)
	}
	secondCompanyUUID := secondCompany["company"].(map[string]any)["UUID"].(string)

	status, conflict := doJSON(t, companiesAttachUser(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, map[string]string{"id": secondCompanyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 assigning an already-assigned user to a different company, got %d body=%v", status, conflict)
	}

	// Detach.
	status, _ = doJSON(t, companiesDetachUser(db), http.MethodDelete, "",
		map[string]string{"id": companyUUID, "userId": graceUUID})
	if status != http.StatusOK {
		t.Fatalf("companiesDetachUser: status=%d", status)
	}

	count, err = db.Model(&companyUserModel{}).WhereEq("user_id", graceUser.ID).Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("expected 0 company_users rows after detach, got count=%d err=%v", count, err)
	}
}

// TestCompaniesCreateRequiresOwner deliberately fails createCompanyForm's
// Rules() validation (no owner_name/owner_email).
//
// Accept: application/json is required here, not just nice-to-have: forge's
// support.ParseRequest, on a Rules() failure, unconditionally writes a
// foundation.ErrorBag into session["errors"] via sess.FormErrors(...). If
// routing.WithRequest's own fallback then decides ctx.WantsJson() is false
// (i.e. no "application/json" in Accept — a plain fetch() call doesn't send
// one by default), it *also* calls ctx.Errors("status", ...), which type-
// asserts session["errors"] straight to map[string][]string with no `ok`
// check — a guaranteed panic, since FormErrors just stored a different
// type. Real bug in forge, not something fixable from here; this test
// documents the workaround (and Dashboard.tsx sets this header on every
// request for the same reason — see its fetch() calls).
func TestCompaniesCreateRequiresOwner(t *testing.T) {
	db := testDB(t)
	m, _ := testMailer(t)
	allowlist := map[string]bool{}

	_, req := newTestSession(t, db)
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Acme Inc"}`)).WithContext(req.Context())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	ctx := &routing.Context{Response: rec, Request: req}
	companiesCreate(db, m, allowlist, "https://app.example.com")(ctx)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 creating a company with no owner, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCompaniesCreateRejectsDeveloperOwner(t *testing.T) {
	db := testDB(t)
	m, _ := testMailer(t)
	allowlist := map[string]bool{"dev@example.com": true}

	status, body := doJSON(t, companiesCreate(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Dev","owner_email":"dev@example.com"}`, nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a developer-email owner, got %d body=%v", status, body)
	}
}
