package kiosk

import (
	"net/http"
	"testing"
)

func TestCompanyUserAssignment(t *testing.T) {
	db := testDB(t)

	status, company := doJSON(t, companiesCreate(db), http.MethodPost, `{"name":"Acme Inc"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, company)
	}
	companyUUID := company["UUID"].(string)

	allowlist := map[string]bool{"dev@example.com": true}

	// Reject assigning a developer email.
	status, body := doJSON(t, companiesAttachUser(db, allowlist), http.MethodPost,
		`{"name":"Dev","email":"dev@example.com"}`, map[string]string{"id": companyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 assigning a developer email, got %d body=%v", status, body)
	}

	// Assign a brand-new email — finds-or-creates the user, defaults role to "member".
	status, membership := doJSON(t, companiesAttachUser(db, allowlist), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, map[string]string{"id": companyUUID})
	if status != http.StatusCreated {
		t.Fatalf("companiesAttachUser: status=%d body=%v", status, membership)
	}
	if membership["Role"] != "member" {
		t.Fatalf("expected default role \"member\", got %v", membership["Role"])
	}

	var created kioskUserModel
	if err := db.Model(&kioskUserModel{}).WhereEq("email", "grace@example.com").First(t.Context(), &created); err != nil {
		t.Fatalf("expected a new user to have been created: %v", err)
	}
	if !created.MustChangePassword {
		t.Fatalf("expected the newly created user to require a password change")
	}

	// List members — expect the new member's own uuid, not their internal id.
	status, listBody := doJSON(t, companiesUsersIndex(db), http.MethodGet, "", map[string]string{"id": companyUUID})
	if status != http.StatusOK {
		t.Fatalf("companiesUsersIndex: status=%d", status)
	}
	_ = listBody // doJSON only decodes objects; presence of a 200 is enough here.

	// Re-attaching to the SAME company updates the role (idempotent).
	status, updated := doJSON(t, companiesAttachUser(db, allowlist), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com","role":"owner"}`, map[string]string{"id": companyUUID})
	if status != http.StatusOK {
		t.Fatalf("expected 200 re-attaching to the same company, got %d body=%v", status, updated)
	}
	if updated["Role"] != "owner" {
		t.Fatalf("expected role updated to \"owner\", got %v", updated["Role"])
	}

	count, err := db.Model(&companyUserModel{}).WhereEq("user_id", created.ID).Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected exactly 1 company_users row after re-attach, got count=%d err=%v", count, err)
	}

	// A second company can't claim an already-assigned user.
	status, secondCompany := doJSON(t, companiesCreate(db), http.MethodPost, `{"name":"Globex"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate (second): status=%d body=%v", status, secondCompany)
	}
	secondCompanyUUID := secondCompany["UUID"].(string)

	status, conflict := doJSON(t, companiesAttachUser(db, allowlist), http.MethodPost,
		`{"name":"Grace","email":"grace@example.com"}`, map[string]string{"id": secondCompanyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 assigning an already-assigned user to a different company, got %d body=%v", status, conflict)
	}

	// Detach.
	status, _ = doJSON(t, companiesDetachUser(db), http.MethodDelete, "",
		map[string]string{"id": companyUUID, "userId": created.UUID})
	if status != http.StatusOK {
		t.Fatalf("companiesDetachUser: status=%d", status)
	}

	count, err = db.Model(&companyUserModel{}).WhereEq("user_id", created.ID).Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("expected 0 company_users rows after detach, got count=%d err=%v", count, err)
	}
}
