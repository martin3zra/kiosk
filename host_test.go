package kiosk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// hostTenancy maps Kiosk onto a host schema with deliberately different
// names (workspaces / workspace_members / slug / is_admin), like Promptly's.
func hostTenancy() Tenancy {
	return Tenancy{
		Mode: Host,
		Tenants: TenantTable{
			Table: "workspaces", Key: "slug", Name: "name", Status: "status", PlanID: "plan_id",
		},
		Members: MemberTable{
			Table: "workspace_members", TenantID: "workspace_id", UserID: "user_id", AdminColumn: "is_admin",
		},
		CreateURL: "/platform/invites",
		IsDeveloper: func(user map[string]any) bool {
			return user["role"] == "developer"
		},
	}
}

type hostFixture struct {
	db    *playsql.DB
	h     *hostKiosk
	ws    int64
	admin kioskUserModel
	other kioskUserModel
}

func newHostFixture(t *testing.T) *hostFixture {
	t.Helper()
	db := testDB(t)
	ctx := t.Context()
	for _, stmt := range []string{
		`CREATE TABLE workspaces (
			id INTEGER NOT NULL PRIMARY KEY,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			plan_id INTEGER
		)`,
		`CREATE TABLE workspace_members (
			id INTEGER NOT NULL PRIMARY KEY,
			workspace_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			is_admin BOOLEAN NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE plans ADD COLUMN code TEXT`,
		`INSERT INTO workspaces (id, slug, name) VALUES (1, 'acme', 'Acme'), (2, 'beta', 'Beta')`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("host schema: %v", err)
		}
	}

	f := &hostFixture{db: db, ws: 1}
	f.admin = seedUser(t, db, "Ada", "ada@example.com", "user")
	f.other = seedUser(t, db, "Bo", "bo@example.com", "user")
	for _, m := range []struct {
		user  int
		admin bool
	}{{f.admin.ID, true}, {f.other.ID, false}} {
		if _, err := db.Exec(ctx, "INSERT INTO workspace_members (workspace_id, user_id, is_admin) VALUES (?, ?, ?)", 1, m.user, m.admin); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}

	f.h = newHostKiosk(Options{DB: db, Allowlist: map[string]bool{"dev@example.com": true}, Tenancy: hostTenancy()})
	return f
}

func seedUser(t *testing.T, db *playsql.DB, name, email, role string) kioskUserModel {
	t.Helper()
	var u kioskUserModel
	if err := db.Model(&kioskUserModel{}).Create(t.Context(), &u, map[string]any{
		"uuid": uuid.NewString(), "name": name, "email": email, "password": "x", "role": role,
	}); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return u
}

// doJSONList is doJSON for handlers that answer with a JSON array.
func doJSONList(t *testing.T, handler routing.HandlerFunc, params map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler(&routing.Context{Response: rec, Request: req, Params: params})
	return rec.Code, rec.Body.String()
}

func TestTenancyValidate(t *testing.T) {
	if err := (Tenancy{}).validate(); err != nil {
		t.Fatalf("Owned needs no mapping, got %v", err)
	}
	if err := hostTenancy().validate(); err != nil {
		t.Fatalf("valid Host mapping rejected: %v", err)
	}

	missing := hostTenancy()
	missing.Members.AdminColumn = ""
	if err := missing.validate(); err == nil || !strings.Contains(err.Error(), "Members.AdminColumn") {
		t.Fatalf("expected a missing-column error naming the field, got %v", err)
	}

	injected := hostTenancy()
	injected.Tenants.Name = "name; DROP TABLE users"
	if err := injected.validate(); err == nil {
		t.Fatal("expected a non-identifier name to be rejected")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("newHostKiosk should panic on an invalid mapping")
		}
	}()
	newHostKiosk(Options{Tenancy: missing})
}

func TestHostCheckSchemaReportsWrongColumn(t *testing.T) {
	f := newHostFixture(t)
	if err := f.h.checkSchema(t.Context()); err != nil {
		t.Fatalf("schema check on a matching mapping: %v", err)
	}

	wrong := hostTenancy()
	wrong.Members.AdminColumn = "is_owner"
	h := newHostKiosk(Options{DB: f.db, Tenancy: wrong})
	err := h.checkSchema(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Tenancy.Members") {
		t.Fatalf("expected a Members mapping error, got %v", err)
	}
}

func TestHostCompaniesStatusAndPlan(t *testing.T) {
	f := newHostFixture(t)

	status, body := doJSONList(t, f.h.index, nil)
	if status != http.StatusOK || !strings.Contains(body, `"uuid":"acme"`) || !strings.Contains(body, `"uuid":"beta"`) {
		t.Fatalf("index: status=%d body=%s", status, body)
	}

	status, row := doJSON(t, f.h.show, http.MethodGet, "", map[string]string{"id": "acme"})
	if status != http.StatusOK || row["name"] != "Acme" || row["status"] != "active" {
		t.Fatalf("show: status=%d body=%v", status, row)
	}
	status, _ = doJSON(t, f.h.show, http.MethodGet, "", map[string]string{"id": "nope"})
	if status != http.StatusNotFound {
		t.Fatalf("unknown key: expected 404, got %d", status)
	}

	status, row = doJSON(t, routing.WithRequest(f.h.updateStatus), http.MethodPut, `{"status":"suspended"}`, map[string]string{"id": "acme"})
	if status != http.StatusOK || row["status"] != "suspended" {
		t.Fatalf("suspend: status=%d body=%v", status, row)
	}
	status, _ = doJSON(t, routing.WithRequest(f.h.updateStatus), http.MethodPut, `{"status":"bogus"}`, map[string]string{"id": "acme"})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad status: expected 422, got %d", status)
	}

	status, plan := doJSON(t, plansCreate(f.db), http.MethodPost, `{"name":"Hosted","price_cents":0,"code":" Hosted "}`, nil)
	if status != http.StatusCreated || plan["Code"] != "hosted" {
		t.Fatalf("plansCreate with code: status=%d body=%v", status, plan)
	}
	planID := strconv.Itoa(int(plan["ID"].(float64)))

	status, row = doJSON(t, routing.WithRequest(f.h.updatePlan), http.MethodPut, `{"plan_id":`+planID+`}`, map[string]string{"id": "acme"})
	if status != http.StatusOK || row["plan"] != "Hosted" {
		t.Fatalf("plan change: status=%d body=%v", status, row)
	}
	status, _ = doJSON(t, routing.WithRequest(f.h.updatePlan), http.MethodPut, `{"plan_id":999}`, map[string]string{"id": "acme"})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown plan: expected 422, got %d", status)
	}

	// Creation belongs to the host.
	status, out := doJSON(t, f.h.create, http.MethodPost, `{}`, nil)
	if status != http.StatusMethodNotAllowed || out["url"] != "/platform/invites" {
		t.Fatalf("create: status=%d body=%v", status, out)
	}
}

func TestHostMembersRolesAndLastAdmin(t *testing.T) {
	f := newHostFixture(t)
	acme := map[string]string{"id": "acme"}

	status, body := doJSONList(t, f.h.usersIndex, acme)
	if status != http.StatusOK || !strings.Contains(body, `"email":"ada@example.com","role":"admin"`) ||
		!strings.Contains(body, `"email":"bo@example.com","role":"member"`) {
		t.Fatalf("members: status=%d body=%s", status, body)
	}

	update := routing.WithRequest(f.h.updateUser)
	adaParams := map[string]string{"id": "acme", "userId": f.admin.UUID}
	boParams := map[string]string{"id": "acme", "userId": f.other.UUID}

	// The only admin can't be demoted or removed.
	status, out := doJSON(t, update, http.MethodPut, `{"role":"member"}`, adaParams)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("demoting the last admin: expected 422, got %d %v", status, out)
	}
	status, _ = doJSON(t, f.h.removeUser, http.MethodDelete, "", adaParams)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("removing the last admin: expected 422, got %d", status)
	}

	// Promote Bo; now Ada can step down.
	status, out = doJSON(t, update, http.MethodPut, `{"role":"admin"}`, boParams)
	if status != http.StatusOK || out["role"] != "admin" {
		t.Fatalf("promote: status=%d body=%v", status, out)
	}
	status, out = doJSON(t, update, http.MethodPut, `{"role":"member"}`, adaParams)
	if status != http.StatusOK || out["role"] != "member" {
		t.Fatalf("demote with another admin: status=%d body=%v", status, out)
	}

	status, _ = doJSON(t, update, http.MethodPut, `{"role":"owner"}`, adaParams)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown role: expected 422, got %d", status)
	}

	// A member who isn't an admin can be removed.
	status, _ = doJSON(t, f.h.removeUser, http.MethodDelete, "", adaParams)
	if status != http.StatusOK {
		t.Fatalf("remove member: expected 200, got %d", status)
	}
	if _, err := f.h.member(t.Context(), f.ws, f.admin.UUID); err == nil {
		t.Fatal("expected Ada's membership to be gone")
	}
}

func TestHostAddUserUsesHostInvite(t *testing.T) {
	f := newHostFixture(t)
	add := routing.WithRequest(f.h.addUser)
	acme := map[string]string{"id": "acme"}

	// No InviteMember hook: adding someone new isn't possible.
	status, _ := doJSON(t, add, http.MethodPost, `{"name":"Cy","email":"cy@example.com"}`, acme)
	if status != http.StatusNotFound {
		t.Fatalf("without a hook: expected 404, got %d", status)
	}

	type call struct {
		tenant      int64
		email, name string
		admin       bool
	}
	var calls []call
	tn := hostTenancy()
	tn.InviteMember = func(_ context.Context, tenantID int64, email, name string, admin bool) error {
		calls = append(calls, call{tenantID, email, name, admin})
		return nil
	}
	f.h = newHostKiosk(Options{DB: f.db, Allowlist: map[string]bool{"dev@example.com": true}, Tenancy: tn})
	add = routing.WithRequest(f.h.addUser)

	status, out := doJSON(t, add, http.MethodPost, `{"name":"Cy","email":" CY@example.com ","role":"member"}`, acme)
	if status != http.StatusAccepted || out["invited"] != true {
		t.Fatalf("invite: status=%d body=%v", status, out)
	}
	if len(calls) != 1 || calls[0] != (call{1, "cy@example.com", "Cy", false}) {
		t.Fatalf("InviteMember called with %+v", calls)
	}

	// An existing member just gets the role.
	status, out = doJSON(t, add, http.MethodPost, `{"name":"Bo","email":"bo@example.com","role":"admin"}`, acme)
	if status != http.StatusOK || out["role"] != "admin" || len(calls) != 1 {
		t.Fatalf("existing member: status=%d body=%v calls=%d", status, out, len(calls))
	}

	// Developers, by either test, can't be members.
	seedUser(t, f.db, "Dev", "dev@example.com", "user")
	seedUser(t, f.db, "Root", "root@example.com", "developer")
	for _, email := range []string{"dev@example.com", "root@example.com"} {
		status, _ = doJSON(t, add, http.MethodPost, `{"name":"D","email":"`+email+`"}`, acme)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("developer %s as member: expected 422, got %d", email, status)
		}
	}
}

func TestDeveloperCheckNeedsBoth(t *testing.T) {
	dev := developerCheck{
		allowlist:   map[string]bool{"dev@example.com": true},
		isDeveloper: func(u map[string]any) bool { return u["role"] == "developer" },
	}
	cases := []struct {
		email, role string
		want        bool
	}{
		{"dev@example.com", "developer", true},
		{"DEV@example.com", "developer", true},
		{"dev@example.com", "user", false},        // listed, wrong role
		{"other@example.com", "developer", false}, // role, not listed
	}
	for _, c := range cases {
		if got := dev.check(map[string]any{"role": c.role}, c.email); got != c.want {
			t.Errorf("check(%s, %s) = %v, want %v", c.email, c.role, got, c.want)
		}
	}
	if (developerCheck{allowlist: dev.allowlist}).check(nil, "dev@example.com") != true {
		t.Error("without IsDeveloper, the allowlist alone decides")
	}
}

func TestHostImpersonationLeavesCompanyUnset(t *testing.T) {
	f := newHostFixture(t)
	developer := seedUser(t, f.db, "Dev", "dev@example.com", "developer")
	root := seedUser(t, f.db, "Root", "root@example.com", "developer") // not allowlisted

	sess, req := newTestSession(t, f.db)
	sess.Put("user_id", developer.ID)
	sess.Put("user", sessionUserPayload(developer))
	start := impersonateStartWith(f.db, f.h.dev, false)

	rec := httptest.NewRecorder()
	start(&routing.Context{Response: rec, Request: req, Params: map[string]string{"id": root.UUID}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("impersonating a role=developer user: expected 403, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	start(&routing.Context{Response: rec, Request: req, Params: map[string]string{"id": f.admin.UUID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("impersonate workspace admin: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sess.Get("company_id") != nil {
		t.Fatalf("Host mode must leave company_id unset, got %v", sess.Get("company_id"))
	}
}

func TestPlanInstallHostMode(t *testing.T) {
	root := t.TempDir()
	plan, err := PlanInstallMode(root, Host)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		if hostSkips[filepath.Base(f.Path)] {
			t.Errorf("Host mode planned %s", f.Path)
		}
	}
	var hasPlans, hasCode bool
	for _, f := range plan.Files {
		hasPlans = hasPlans || strings.HasSuffix(f.Path, "create_plans_table.yaml")
		hasCode = hasCode || strings.HasSuffix(f.Path, "add_code_to_plans_table.yaml")
	}
	if !hasPlans || !hasCode {
		t.Fatalf("Host mode must still copy the plans migrations (plans=%v code=%v)", hasPlans, hasCode)
	}

	if _, err := ApplyInstall(root, plan, nil); err != nil {
		t.Fatal(err)
	}
	lockData, _ := os.ReadFile(filepath.Join(root, lockFileName))
	if !strings.Contains(string(lockData), `"mode": "host"`) {
		t.Fatalf("lock should record the mode:\n%s", lockData)
	}

	// A later kiosk:update (PlanInstall, no mode given) keeps Host mode.
	again, err := PlanInstall(root)
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode != Host || len(again.Files) != len(plan.Files) {
		t.Fatalf("re-plan: mode=%v files=%d, want host/%d", again.Mode, len(again.Files), len(plan.Files))
	}
	if _, err := os.Stat(filepath.Join(root, "database", "20260821120001_create_companies_table.yaml")); err == nil {
		t.Fatal("Host mode wrote the companies migration")
	}
}
