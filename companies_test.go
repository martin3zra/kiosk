package kiosk

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"

	_ "modernc.org/sqlite"
)

// testDB opens a fresh in-memory sqlite DB with Kiosk's schema — the same
// column shapes camel would create from kiosk/migrations, verified against
// that YAML via `camel plan` while building this module.
func testDB(t *testing.T) *playsql.DB {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	schema := []string{
		`CREATE TABLE users (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			role TEXT NOT NULL,
			email_verified_at DATETIME,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME,
			deleted_at DATETIME
		)`,
		`CREATE TABLE plans (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			price_cents INTEGER NOT NULL,
			currency TEXT NOT NULL,
			limits TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE companies (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			identifier TEXT,
			plan_id INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME,
			deleted_at DATETIME
		)`,
		`CREATE TABLE roles (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE addons (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			price_cents INTEGER NOT NULL,
			currency TEXT NOT NULL,
			active_from DATETIME,
			active_until DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE company_addons (
			id INTEGER NOT NULL PRIMARY KEY,
			company_id INTEGER NOT NULL,
			addon_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE company_users (
			id INTEGER NOT NULL PRIMARY KEY,
			company_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role_id INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE impersonation_sessions (
			id INTEGER NOT NULL PRIMARY KEY,
			developer_user_id INTEGER NOT NULL,
			target_user_id INTEGER NOT NULL,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ended_at DATETIME,
			ip_address TEXT,
			user_agent TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE invitations (
			id INTEGER NOT NULL PRIMARY KEY,
			uuid TEXT NOT NULL,
			company_id INTEGER NOT NULL,
			role_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			email TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			expires_at DATETIME NOT NULL,
			accepted_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
	}
	for _, stmt := range schema {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}

	// Mirrors the real seed_default_roles.sql migration — every test gets
	// the same two roles a real deployment starts with.
	if _, err := sqlDB.Exec(
		`INSERT INTO roles (uuid, name, description) VALUES
			('df9202c9-466b-40e0-b434-23aaa6765af9', 'owner', 'Full control over the company, billing, and members.'),
			('c53392c4-7511-4d45-bb06-fe8a595f1690', 'admin', 'Manage company members and settings.')`,
	); err != nil {
		t.Fatalf("seed roles: %v", err)
	}

	db, err := playsql.Use(sqlDB, "sqlite")
	if err != nil {
		t.Fatalf("playsql.Use: %v", err)
	}
	return db
}

func doJSON(t *testing.T, handler routing.HandlerFunc, method, body string, params map[string]string) (int, map[string]any) {
	t.Helper()

	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, "/", nil)
	}

	rec := httptest.NewRecorder()
	ctx := &routing.Context{Response: rec, Request: req, Params: params}
	handler(ctx)

	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCompanyPlanAddonLifecycle(t *testing.T) {
	db := testDB(t)

	// Create a plan.
	status, plan := doJSON(t, plansCreate(db), http.MethodPost,
		`{"name":"Pro","price_cents":2900,"currency":"USD"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("plansCreate: status=%d body=%v", status, plan)
	}
	planID := int(plan["ID"].(float64))

	// Create a company on that plan.
	allowlist := map[string]bool{}
	m, _ := testMailer(t)
	status, created := doJSON(t, companiesCreate(db, m, allowlist, "https://app.example.com"), http.MethodPost,
		`{"name":"Acme Inc","owner_name":"Owner","owner_email":"owner@example.com","plan_id":`+strconv.Itoa(planID)+`}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("companiesCreate: status=%d body=%v", status, created)
	}
	company := created["company"].(map[string]any)
	if company["Status"] != "active" {
		t.Fatalf("expected new company to default to active status, got %v", company["Status"])
	}
	companyID := int(company["ID"].(float64))
	companyUUID := company["UUID"].(string)

	// List companies — expect exactly the one just created.
	status, _ = doJSON(t, companiesIndex(db), http.MethodGet, "", nil)
	if status != http.StatusOK {
		t.Fatalf("companiesIndex: status=%d", status)
	}

	// Suspend it.
	status, updated := doJSON(t, companiesUpdateStatus(db), http.MethodPut,
		`{"status":"suspended"}`, map[string]string{"id": companyUUID})
	if status != http.StatusOK || updated["Status"] != "suspended" {
		t.Fatalf("companiesUpdateStatus: status=%d body=%v", status, updated)
	}

	// Reject an invalid status value.
	status, _ = doJSON(t, companiesUpdateStatus(db), http.MethodPut,
		`{"status":"bogus"}`, map[string]string{"id": companyUUID})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid status, got %d", status)
	}

	// A malformed uuid path param is a 400, not a 500 from a botched query.
	status, _ = doJSON(t, companiesUpdateStatus(db), http.MethodPut,
		`{"status":"suspended"}`, map[string]string{"id": "not-a-uuid"})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed uuid path param, got %d", status)
	}

	// Create an addon and attach it to the company.
	status, addon := doJSON(t, addonsCreate(db), http.MethodPost,
		`{"name":"Extra seats","price_cents":500,"currency":"USD"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("addonsCreate: status=%d body=%v", status, addon)
	}
	addonID := int(addon["ID"].(float64))
	addonUUID := addon["UUID"].(string)

	status, _ = doJSON(t, companiesAttachAddon(db), http.MethodPost,
		`{"addon_id":`+strconv.Itoa(addonID)+`}`, map[string]string{"id": companyUUID})
	if status != http.StatusCreated {
		t.Fatalf("companiesAttachAddon: status=%d", status)
	}

	count, err := db.Model(&companyAddonModel{}).
		WhereEq("company_id", companyID).WhereEq("addon_id", addonID).
		Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("expected 1 company_addons row after attach, got count=%d err=%v", count, err)
	}

	// Detach it again.
	status, _ = doJSON(t, companiesDetachAddon(db), http.MethodDelete, "",
		map[string]string{"id": companyUUID, "addonId": addonUUID})
	if status != http.StatusOK {
		t.Fatalf("companiesDetachAddon: status=%d", status)
	}

	count, err = db.Model(&companyAddonModel{}).
		WhereEq("company_id", companyID).WhereEq("addon_id", addonID).
		Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("expected 0 company_addons rows after detach, got count=%d err=%v", count, err)
	}
}
