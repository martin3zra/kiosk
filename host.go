package kiosk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/martin3zra/forge/foundation"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/forge/support"
	"github.com/martin3zra/playsql"
)

// ErrLastAdmin is returned when a Host-mode change would leave a tenant with
// no admin: demoting or removing its only one.
var ErrLastAdmin = errors.New("kiosk: a company keeps at least one admin")

// hostKiosk serves the company and member routes in Host mode, reading and
// writing the host's own tables through Tenancy's mapping. Mapped names are
// validated identifiers (Tenancy.validate); every value is a bind argument.
type hostKiosk struct {
	db       *playsql.DB
	t        Tenancy
	dev      developerCheck
	homeURL  string
	schemaOK atomic.Bool
}

func newHostKiosk(opts Options) *hostKiosk {
	if err := opts.Tenancy.validate(); err != nil {
		panic(err)
	}
	return &hostKiosk{
		db:      opts.DB,
		t:       opts.Tenancy,
		dev:     developerCheck{allowlist: opts.Allowlist, isDeveloper: opts.Tenancy.IsDeveloper},
		homeURL: opts.homeURL(),
	}
}

// hostTenant is one mapped tenant row.
type hostTenant struct {
	ID     int64  `db:"id"`
	Key    string `db:"tenant_key"`
	Name   string `db:"name"`
	Status string `db:"status"`
	PlanID *int64 `db:"plan_id"`
}

// hostMember is one mapped membership joined with its user.
type hostMember struct {
	UserID  int64  `db:"user_id"`
	UUID    string `db:"uuid"`
	Name    string `db:"name"`
	Email   string `db:"email"`
	IsAdmin int    `db:"is_admin"`
}

func (m hostMember) role() string {
	if m.IsAdmin != 0 {
		return "admin"
	}
	return "member"
}

// --- queries -----------------------------------------------------------------

func (h *hostKiosk) tenantSelect() string {
	tt := h.t.Tenants
	status, plan := "'active'", "NULL"
	if tt.Status != "" {
		status = tt.Status
	}
	if tt.PlanID != "" {
		plan = tt.PlanID
	}
	q := fmt.Sprintf("SELECT id, %s AS tenant_key, %s AS name, %s AS status, %s AS plan_id FROM %s",
		tt.Key, tt.Name, status, plan, tt.Table)
	if tt.SoftDelete != "" {
		q += " WHERE " + tt.SoftDelete + " IS NULL"
	} else {
		q += " WHERE 1 = 1"
	}
	return q
}

// checkSchema runs each mapped column once in a no-row SELECT, so a wrong
// mapping fails with a clear message instead of a confusing query error
// deep in a handler. It retries until it passes once (the host may route
// before its migrations have run, e.g. a CLI command).
func (h *hostKiosk) checkSchema(ctx context.Context) error {
	if h.schemaOK.Load() {
		return nil
	}
	if _, err := h.db.Exec(ctx, h.tenantSelect()+" AND 1 = 0"); err != nil {
		return fmt.Errorf("kiosk: Tenancy.Tenants mapping doesn't match table %s: %w", h.t.Tenants.Table, err)
	}
	mt := h.t.Members
	if _, err := h.db.Exec(ctx, fmt.Sprintf("SELECT %s, %s, %s FROM %s WHERE 1 = 0",
		mt.TenantID, mt.UserID, mt.AdminColumn, mt.Table)); err != nil {
		return fmt.Errorf("kiosk: Tenancy.Members mapping doesn't match table %s: %w", mt.Table, err)
	}
	h.schemaOK.Store(true)
	return nil
}

func (h *hostKiosk) tenants(ctx context.Context) ([]hostTenant, error) {
	var rows []hostTenant
	err := h.db.Raw(ctx, &rows, h.tenantSelect()+" ORDER BY "+h.t.Tenants.Name)
	return rows, err
}

func (h *hostKiosk) tenant(ctx context.Context, key string) (hostTenant, error) {
	var rows []hostTenant
	if err := h.db.Raw(ctx, &rows, h.tenantSelect()+" AND "+h.t.Tenants.Key+" = ?", key); err != nil {
		return hostTenant{}, err
	}
	if len(rows) == 0 {
		return hostTenant{}, playsql.ErrNotFound
	}
	return rows[0], nil
}

func (h *hostKiosk) members(ctx context.Context, tenantID int64) ([]hostMember, error) {
	mt := h.t.Members
	var rows []hostMember
	err := h.db.Raw(ctx, &rows, fmt.Sprintf(
		"SELECT u.id AS user_id, u.uuid AS uuid, u.name AS name, u.email AS email, "+
			"CASE WHEN m.%s THEN 1 ELSE 0 END AS is_admin "+
			"FROM %s m JOIN users u ON u.id = m.%s WHERE m.%s = ? ORDER BY u.name",
		mt.AdminColumn, mt.Table, mt.UserID, mt.TenantID), tenantID)
	return rows, err
}

func (h *hostKiosk) member(ctx context.Context, tenantID int64, userUUID string) (hostMember, error) {
	all, err := h.members(ctx, tenantID)
	if err != nil {
		return hostMember{}, err
	}
	for _, m := range all {
		if m.UUID == userUUID {
			return m, nil
		}
	}
	return hostMember{}, playsql.ErrNotFound
}

// otherAdmins counts the tenant's admins besides userID.
func (h *hostKiosk) otherAdmins(ctx context.Context, tenantID, userID int64) (int, error) {
	all, err := h.members(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range all {
		if m.IsAdmin != 0 && m.UserID != userID {
			n++
		}
	}
	return n, nil
}

// SetAdmin and Remove run as one statement each; the last-admin guard reads
// first. A race between two developers demoting the last two admins at the
// same moment is accepted — the host's own member screens can fix it.
func (h *hostKiosk) setAdmin(ctx context.Context, tenantID int64, m hostMember, admin bool) error {
	if !admin && m.IsAdmin != 0 {
		n, err := h.otherAdmins(ctx, tenantID, m.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	mt := h.t.Members
	_, err := h.db.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ? AND %s = ?",
		mt.Table, mt.AdminColumn, mt.TenantID, mt.UserID), admin, tenantID, m.UserID)
	return err
}

func (h *hostKiosk) remove(ctx context.Context, tenantID int64, m hostMember) error {
	if m.IsAdmin != 0 {
		n, err := h.otherAdmins(ctx, tenantID, m.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	mt := h.t.Members
	_, err := h.db.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = ? AND %s = ?",
		mt.Table, mt.TenantID, mt.UserID), tenantID, m.UserID)
	return err
}

// --- routes ------------------------------------------------------------------

// register mounts the Host-mode company routes. Plans, addons, users and
// impersonation are shared with Owned mode (see Register).
func (h *hostKiosk) register(rg *routing.Router) {
	rg.GET("", h.withSchema(h.dashboard))
	rg.GET("/roles", h.roles)

	rg.GET("/companies", h.withSchema(h.index))
	rg.POST("/companies", h.create)
	rg.GET("/companies/:id", h.withSchema(h.show))
	rg.PUT("/companies/:id/status", h.withSchema(routing.WithRequest(h.updateStatus)))
	rg.PUT("/companies/:id/plan", h.withSchema(routing.WithRequest(h.updatePlan)))

	rg.GET("/companies/:id/users", h.withSchema(h.usersIndex))
	rg.POST("/companies/:id/users", h.withSchema(routing.WithRequest(h.addUser)))
	rg.PUT("/companies/:id/users/:userId", h.withSchema(routing.WithRequest(h.updateUser)))
	rg.DELETE("/companies/:id/users/:userId", h.withSchema(h.removeUser))

	// The host lists and resends its own invitations; an empty list keeps
	// the dashboard's members panel working unchanged.
	rg.GET("/companies/:id/invitations", func(ctx *routing.Context) { ctx.JSON(http.StatusOK, []any{}) })

	rg.POST("/companies/:id/addons", h.withSchema(routing.WithRequest(h.attachAddon)))
	rg.DELETE("/companies/:id/addons/:addonId", h.withSchema(h.detachAddon))
}

func (h *hostKiosk) withSchema(next routing.HandlerFunc) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		if err := h.checkSchema(ctx.Request.Context()); err != nil {
			ctx.Error(err)
			return
		}
		next(ctx)
	}
}

// found resolves :id (the tenant's Key) or writes a 404.
func (h *hostKiosk) found(ctx *routing.Context) (hostTenant, bool) {
	t, err := h.tenant(ctx.Request.Context(), ctx.Params["id"])
	if errors.Is(err, playsql.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "company not found"})
		return t, false
	}
	if err != nil {
		ctx.Error(err)
		return t, false
	}
	return t, true
}

type hostCompanyRow struct {
	UUID   string `json:"uuid"` // the tenant's Key: what every /companies/:id path takes
	Key    string `json:"key"`
	Name   string `json:"name"`
	Status string `json:"status"`
	PlanID *int64 `json:"plan_id"`
	Plan   string `json:"plan"`
}

func (h *hostKiosk) rows(ctx context.Context, tenants []hostTenant) ([]hostCompanyRow, []planModel, error) {
	var plans []planModel
	if h.t.Tenants.PlanID != "" {
		if err := h.db.Model(&planModel{}).OrderBy("name", playsql.Asc).Get(ctx, &plans); err != nil {
			return nil, nil, err
		}
	}
	names := make(map[int64]string, len(plans))
	for _, p := range plans {
		names[int64(p.ID)] = p.Name
	}
	out := make([]hostCompanyRow, len(tenants))
	for i, t := range tenants {
		plan := "—"
		if t.PlanID != nil {
			if n, ok := names[*t.PlanID]; ok {
				plan = n
			}
		}
		out[i] = hostCompanyRow{UUID: t.Key, Key: t.Key, Name: t.Name, Status: t.Status, PlanID: t.PlanID, Plan: plan}
	}
	return out, plans, nil
}

func (h *hostKiosk) dashboard(ctx *routing.Context) {
	reqCtx := ctx.Request.Context()
	tenants, err := h.tenants(reqCtx)
	if err != nil {
		ctx.Error(err)
		return
	}
	rows, plans, err := h.rows(reqCtx, tenants)
	if err != nil {
		ctx.Error(err)
		return
	}
	userCount, err := h.db.Model(&kioskUserModel{}).Count(reqCtx)
	if err != nil {
		ctx.Error(err)
		return
	}
	type planOption struct {
		ID   int     `json:"id"`
		Name string  `json:"name"`
		Code *string `json:"code"`
	}
	options := make([]planOption, len(plans))
	for i, p := range plans {
		options[i] = planOption{ID: p.ID, Name: p.Name, Code: p.Code}
	}
	ctx.Render("Kiosk/Dashboard", map[string]any{
		"companies": rows,
		"stats": map[string]any{
			"companies": len(rows),
			"plans":     len(plans),
			"users":     userCount,
		},
		"host": map[string]any{
			"create_url":  h.t.CreateURL,
			"home_url":    h.homeURL,
			"can_invite":  h.t.InviteMember != nil,
			"can_suspend": h.t.Tenants.Status != "",
			"plans":       options,
		},
	})
}

// roles stands in for Kiosk's roles table: Host mode only knows the admin
// flag. Same JSON shape as roleModel so the dashboard's picker is unchanged.
func (h *hostKiosk) roles(ctx *routing.Context) {
	ctx.JSON(http.StatusOK, []map[string]any{
		{"UUID": "admin", "Name": "admin", "Description": "Manage the company's members and settings."},
		{"UUID": "member", "Name": "member", "Description": nil},
	})
}

func (h *hostKiosk) index(ctx *routing.Context) {
	tenants, err := h.tenants(ctx.Request.Context())
	if err != nil {
		ctx.Error(err)
		return
	}
	rows, _, err := h.rows(ctx.Request.Context(), tenants)
	if err != nil {
		ctx.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, rows)
}

func (h *hostKiosk) show(ctx *routing.Context) {
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	rows, _, err := h.rows(ctx.Request.Context(), []hostTenant{t})
	if err != nil {
		ctx.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, rows[0])
}

// create: the host owns tenant creation in Host mode.
func (h *hostKiosk) create(ctx *routing.Context) {
	ctx.JSON(http.StatusMethodNotAllowed, map[string]any{
		"error": "companies are created in the app, not in Kiosk",
		"url":   h.t.CreateURL,
	})
}

func (h *hostKiosk) updateStatus(ctx *routing.Context, form *updateCompanyStatusForm) {
	if h.t.Tenants.Status == "" {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "suspending isn't enabled"})
		return
	}
	if form.Status != "active" && form.Status != "suspended" {
		ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "status must be active or suspended"})
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	tt := h.t.Tenants
	if _, err := h.db.Exec(ctx.Request.Context(), fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", tt.Table, tt.Status),
		form.Status, t.ID); err != nil {
		ctx.Error(err)
		return
	}
	h.show(ctx)
}

func (h *hostKiosk) updatePlan(ctx *routing.Context, form *updateCompanyPlanForm) {
	if h.t.Tenants.PlanID == "" {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "plans aren't enabled"})
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	var plan planModel
	if err := h.db.Model(&planModel{}).Find(ctx.Request.Context(), &plan, form.PlanID); err != nil {
		if errors.Is(err, playsql.ErrNotFound) {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "unknown plan"})
			return
		}
		ctx.Error(err)
		return
	}
	tt := h.t.Tenants
	if _, err := h.db.Exec(ctx.Request.Context(), fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", tt.Table, tt.PlanID),
		plan.ID, t.ID); err != nil {
		ctx.Error(err)
		return
	}
	h.show(ctx)
}

func (h *hostKiosk) usersIndex(ctx *routing.Context) {
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	all, err := h.members(ctx.Request.Context(), t.ID)
	if err != nil {
		ctx.Error(err)
		return
	}
	rows := make([]memberRow, len(all))
	for i, m := range all {
		rows[i] = memberRow{UUID: m.UUID, Name: m.Name, Email: m.Email, Role: m.role()}
	}
	ctx.JSON(http.StatusOK, rows)
}

// adminRole reads the picker's role name; "" (the picker's default) is admin,
// matching Owned mode's default.
func adminRole(role string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "admin":
		return true, true
	case "member":
		return false, true
	}
	return false, false
}

// addUser: an existing member gets their role set; anyone else goes through
// the host's own invitation (Tenancy.InviteMember).
func (h *hostKiosk) addUser(ctx *routing.Context, form *assignCompanyUserForm) {
	admin, ok := adminRole(form.Role)
	if !ok {
		ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "role must be admin or member"})
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	reqCtx := ctx.Request.Context()
	email := strings.ToLower(strings.TrimSpace(form.Email))

	var user kioskUserModel
	err := h.db.Model(&kioskUserModel{}).WhereEq("email", email).First(reqCtx, &user)
	switch {
	case err == nil:
		if h.dev.target(user) {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "developer accounts cannot be company members"})
			return
		}
		if m, mErr := h.member(reqCtx, t.ID, user.UUID); mErr == nil {
			h.writeRole(ctx, t, m, admin)
			return
		} else if !errors.Is(mErr, playsql.ErrNotFound) {
			ctx.Error(mErr)
			return
		}
	case !errors.Is(err, playsql.ErrNotFound):
		ctx.Error(err)
		return
	}

	if h.t.InviteMember == nil {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "adding members isn't enabled"})
		return
	}
	if err := h.t.InviteMember(reqCtx, t.ID, email, strings.TrimSpace(form.Name), admin); err != nil {
		ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, map[string]any{"invited": true, "email": email})
}

type updateMemberRoleForm struct {
	support.FormRequest
	Role string `json:"role"`
}

func (f *updateMemberRoleForm) Rules() map[string]any {
	return map[string]any{"role": "required"}
}

func (h *hostKiosk) updateUser(ctx *routing.Context, form *updateMemberRoleForm) {
	admin, ok := adminRole(form.Role)
	if !ok {
		ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": "role must be admin or member"})
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	m, err := h.member(ctx.Request.Context(), t.ID, ctx.Params["userId"])
	if errors.Is(err, playsql.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "not a member"})
		return
	}
	if err != nil {
		ctx.Error(err)
		return
	}
	h.writeRole(ctx, t, m, admin)
}

func (h *hostKiosk) writeRole(ctx *routing.Context, t hostTenant, m hostMember, admin bool) {
	if err := h.setAdmin(ctx.Request.Context(), t.ID, m, admin); err != nil {
		if errors.Is(err, ErrLastAdmin) {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
		ctx.Error(err)
		return
	}
	role := "member"
	if admin {
		role = "admin"
	}
	ctx.JSON(http.StatusOK, memberRow{UUID: m.UUID, Name: m.Name, Email: m.Email, Role: role})
}

func (h *hostKiosk) removeUser(ctx *routing.Context) {
	userUUID, ok := paramUUID(ctx, "userId")
	if !ok {
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	m, err := h.member(ctx.Request.Context(), t.ID, userUUID)
	if errors.Is(err, playsql.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, map[string]any{"error": "not a member"})
		return
	}
	if err != nil {
		ctx.Error(err)
		return
	}
	if err := h.remove(ctx.Request.Context(), t.ID, m); err != nil {
		if errors.Is(err, ErrLastAdmin) {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
		ctx.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"ok": true})
}

// company_addons.company_id holds the host tenant's id in Host mode; the
// host's own migration adds the foreign key Kiosk can't know about.
func (h *hostKiosk) attachAddon(ctx *routing.Context, form *attachAddonForm) {
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	if _, err := h.db.Model(&companyAddonModel{}).Insert(ctx.Request.Context(), map[string]any{
		"company_id": t.ID,
		"addon_id":   form.AddonID,
	}); err != nil {
		ctx.Error(err)
		return
	}
	ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
}

func (h *hostKiosk) detachAddon(ctx *routing.Context) {
	addonUUID, ok := paramUUID(ctx, "addonId")
	if !ok {
		return
	}
	t, ok := h.found(ctx)
	if !ok {
		return
	}
	var addon addonModel
	if err := h.db.Model(&addonModel{}).WhereEq("uuid", addonUUID).First(ctx.Request.Context(), &addon); err != nil {
		ctx.Error(err)
		return
	}
	if _, err := h.db.Model(&companyAddonModel{}).
		WhereEq("company_id", t.ID).WhereEq("addon_id", addon.ID).
		ForceDelete(ctx.Request.Context()); err != nil {
		ctx.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"ok": true})
}

// notFound is what a non-developer gets from /kiosks: the same response as
// a URL that doesn't exist.
func notFound(ctx *routing.Context) {
	ctx.Error(foundation.HTTPError{StatusCode: http.StatusNotFound, Message: "Not Found"})
}
