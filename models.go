package kiosk

import "time"

// companyModel is the on-disk shape of a row in companies. Money columns are
// integer cents (see forge's foundation.MoneyToText), not decimals.
// Identifier is a free-text tax id (e.g. an RNC, EIN, or whatever the host's
// locale uses) — Kiosk doesn't validate its format.
type companyModel struct {
	ID         int        `db:"id"`
	UUID       string     `db:"uuid"`
	Name       string     `db:"name"`
	Status     string     `db:"status"`
	Identifier *string    `db:"identifier"`
	PlanID     *int       `db:"plan_id"`
	CreatedAt  *time.Time `db:"created_at"`
	UpdatedAt  *time.Time `db:"updated_at"`
	DeletedAt  *time.Time `db:"deleted_at"`
}

func (companyModel) TableName() string { return "companies" }

// planModel is the on-disk shape of a row in plans.
type planModel struct {
	ID         int     `db:"id"`
	UUID       string  `db:"uuid"`
	Name       string  `db:"name"`
	PriceCents int64   `db:"price_cents"`
	Currency   string  `db:"currency"`
	Limits     *string `db:"limits"` // raw JSON, unmarshaled by callers that need it
	// Code is an optional stable key a host's code can branch on (e.g.
	// "hosted"), where Name is free to change. Unique when set.
	Code      *string    `db:"code"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (planModel) TableName() string { return "plans" }

// addonModel is the on-disk shape of a row in addons.
type addonModel struct {
	ID          int        `db:"id"`
	UUID        string     `db:"uuid"`
	Name        string     `db:"name"`
	PriceCents  int64      `db:"price_cents"`
	Currency    string     `db:"currency"`
	ActiveFrom  *time.Time `db:"active_from"`
	ActiveUntil *time.Time `db:"active_until"`
	CreatedAt   *time.Time `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

func (addonModel) TableName() string { return "addons" }

// companyAddonModel is the company <-> addon pivot (company_addons).
type companyAddonModel struct {
	ID        int        `db:"id"`
	CompanyID int        `db:"company_id"`
	AddonID   int        `db:"addon_id"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (companyAddonModel) TableName() string { return "company_addons" }

// companyUserModel is the company <-> user pivot (company_users). RoleID
// points at the global roles table (roles.go) — the user's role within that
// company (e.g. "owner", "admin") — distinct from the host app's own
// users.role column (spark's admin/user ACL value), which Kiosk never
// touches. No DB-level foreign key (see the migration that added this
// column): app-enforced, matching CompanyID/UserID on this same table.
type companyUserModel struct {
	ID        int        `db:"id"`
	CompanyID int        `db:"company_id"`
	UserID    int        `db:"user_id"`
	RoleID    *int       `db:"role_id"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (companyUserModel) TableName() string { return "company_users" }

// roleModel is the on-disk shape of a row in roles — a fixed global set
// (not per-company), seeded with "owner" and "admin" by Kiosk's own
// migration. A host project extends the list with its own migration
// inserting more rows into this same table; Kiosk exposes no API for
// adding roles, on purpose (see roles.go).
type roleModel struct {
	ID          int        `db:"id"`
	UUID        string     `db:"uuid"`
	Name        string     `db:"name"`
	Description *string    `db:"description"`
	CreatedAt   *time.Time `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

func (roleModel) TableName() string { return "roles" }

// impersonationSessionModel is the audit trail for impersonate.go's
// start/stop handlers — one row per impersonation, open (ended_at nil)
// while it's in progress.
type impersonationSessionModel struct {
	ID              int        `db:"id"`
	DeveloperUserID int        `db:"developer_user_id"`
	TargetUserID    int        `db:"target_user_id"`
	StartedAt       *time.Time `db:"started_at"`
	EndedAt         *time.Time `db:"ended_at"`
	IPAddress       *string    `db:"ip_address"`
	UserAgent       *string    `db:"user_agent"`
	CreatedAt       *time.Time `db:"created_at"`
	UpdatedAt       *time.Time `db:"updated_at"`
}

func (impersonationSessionModel) TableName() string { return "impersonation_sessions" }

// invitationModel is a pending "join this company" invite — created instead
// of an eager user account whenever companiesAttachUser/companiesCreate is
// given an email that doesn't match an existing user (see
// findOrCreateInvitedCompanyUser in company_users.go). AcceptedAt is nil
// until the invitationsAccept handler fills it in; there's no separate
// "revoked" state, just letting ExpiresAt pass.
type invitationModel struct {
	ID         int        `db:"id"`
	UUID       string     `db:"uuid"`
	CompanyID  int        `db:"company_id"`
	RoleID     int        `db:"role_id"`
	Name       string     `db:"name"`
	Email      string     `db:"email"`
	Token      string     `db:"token"`
	ExpiresAt  *time.Time `db:"expires_at"`
	AcceptedAt *time.Time `db:"accepted_at"`
	CreatedAt  *time.Time `db:"created_at"`
	UpdatedAt  *time.Time `db:"updated_at"`
}

func (invitationModel) TableName() string { return "invitations" }
