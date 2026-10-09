package kiosk

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// TenancyMode picks where Kiosk's companies live.
type TenancyMode int

const (
	// Owned is the default: Kiosk's own companies, company_users, roles and
	// invitations tables, exactly as before Tenancy existed.
	Owned TenancyMode = iota
	// Host points Kiosk at the host app's own tenant and member tables
	// (Tenancy.Tenants and Tenancy.Members). Kiosk still queries them
	// directly through playsql; the mapping only names tables and columns.
	Host
)

func (m TenancyMode) String() string {
	if m == Host {
		return "host"
	}
	return "owned"
}

// ParseTenancyMode reads "owned" or "host" (kiosk:install --mode).
func ParseTenancyMode(s string) (TenancyMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "owned":
		return Owned, nil
	case "host":
		return Host, nil
	}
	return Owned, fmt.Errorf("kiosk: unknown tenancy mode %q (want owned or host)", s)
}

// Tenancy configures Options.Tenancy. The zero value is Owned.
type Tenancy struct {
	Mode    TenancyMode
	Tenants TenantTable // Host only
	Members MemberTable // Host only

	// CreateURL replaces "New company" in Host mode: the host owns tenant
	// creation, so Kiosk links there instead (e.g. "/platform/invites").
	CreateURL string

	// InviteMember runs "Add member" in Host mode, so the host sends its own
	// invitation through its own flow. Nil hides adding members.
	InviteMember func(ctx context.Context, tenantID int64, email, name string, admin bool) error

	// IsDeveloper is checked as well as Options.Allowlist — both must pass —
	// for /kiosks and for "can't impersonate a developer". It receives the
	// session's user payload (the host's Authenticatable, JSON-shaped). Nil
	// means the allowlist alone decides, as before.
	IsDeveloper func(user map[string]any) bool
}

// TenantTable maps "company" onto the host's tenant table.
type TenantTable struct {
	Table string // e.g. "workspaces"; its primary key must be "id"
	Key   string // unique, URL-safe column used in /kiosks/companies/:id, e.g. "slug"
	Name  string // display name column, e.g. "name"

	// Status holds "active" | "suspended". Empty hides suspend/reactivate
	// and reports every tenant as active.
	Status string
	// PlanID references plans.id. Empty hides plans.
	PlanID string
	// SoftDelete names a nullable deleted-at column; rows with it set are
	// left out. Empty when the host hard-deletes.
	SoftDelete string
}

// MemberTable maps company membership onto the host's pivot table.
type MemberTable struct {
	Table       string // e.g. "workspace_members"
	TenantID    string // e.g. "workspace_id"
	UserID      string // e.g. "user_id"
	AdminColumn string // boolean column; Kiosk shows Admin / Member from it
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// validate checks a Host mapping's shape. Names end up in SQL text, so each
// must be a plain lowercase identifier; they come from the host's code,
// never from a request, and this keeps it that way.
func (t Tenancy) validate() error {
	if t.Mode != Host {
		return nil
	}
	required := map[string]string{
		"Tenants.Table": t.Tenants.Table, "Tenants.Key": t.Tenants.Key, "Tenants.Name": t.Tenants.Name,
		"Members.Table": t.Members.Table, "Members.TenantID": t.Members.TenantID,
		"Members.UserID": t.Members.UserID, "Members.AdminColumn": t.Members.AdminColumn,
	}
	optional := map[string]string{
		"Tenants.Status": t.Tenants.Status, "Tenants.PlanID": t.Tenants.PlanID, "Tenants.SoftDelete": t.Tenants.SoftDelete,
	}
	for field, v := range required {
		if v == "" {
			return fmt.Errorf("kiosk: Tenancy.%s is required in Host mode", field)
		}
		if !identRe.MatchString(v) {
			return fmt.Errorf("kiosk: Tenancy.%s %q is not a plain identifier", field, v)
		}
	}
	for field, v := range optional {
		if v != "" && !identRe.MatchString(v) {
			return fmt.Errorf("kiosk: Tenancy.%s %q is not a plain identifier", field, v)
		}
	}
	return nil
}

// developerCheck answers "is this user a Kiosk developer" from the email
// allowlist and, when set, the host's IsDeveloper. Both must agree.
type developerCheck struct {
	allowlist   map[string]bool
	isDeveloper func(map[string]any) bool
}

func (d developerCheck) check(user map[string]any, email string) bool {
	if email == "" || !d.allowlist[strings.ToLower(email)] {
		return false
	}
	return d.isDeveloper == nil || d.isDeveloper(user)
}

// target reports whether a user row (an impersonation target or a would-be
// member) counts as a developer by EITHER test — the guard errs toward
// refusing.
func (d developerCheck) target(u kioskUserModel) bool {
	if d.allowlist[strings.ToLower(u.Email)] {
		return true
	}
	return d.isDeveloper != nil && d.isDeveloper(sessionUserPayload(u))
}
