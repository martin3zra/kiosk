package kiosk

import "time"

// companyModel is the on-disk shape of a row in companies. Money columns are
// integer cents (see forge's foundation.MoneyToText), not decimals.
type companyModel struct {
	ID        int        `db:"id"`
	UUID      string     `db:"uuid"`
	Name      string     `db:"name"`
	Status    string     `db:"status"`
	PlanID    *int       `db:"plan_id"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

func (companyModel) TableName() string { return "companies" }

// planModel is the on-disk shape of a row in plans.
type planModel struct {
	ID         int        `db:"id"`
	UUID       string     `db:"uuid"`
	Name       string     `db:"name"`
	PriceCents int64      `db:"price_cents"`
	Currency   string     `db:"currency"`
	Limits     *string    `db:"limits"` // raw JSON, unmarshaled by callers that need it
	CreatedAt  *time.Time `db:"created_at"`
	UpdatedAt  *time.Time `db:"updated_at"`
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

// companyUserModel is the company <-> user pivot (company_users). Role here
// is the user's role within that company (e.g. "owner", "member") — distinct
// from the host app's own users.role column (spark's admin/user ACL value),
// which Kiosk never touches.
type companyUserModel struct {
	ID        int        `db:"id"`
	CompanyID int        `db:"company_id"`
	UserID    int        `db:"user_id"`
	Role      string     `db:"role"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

func (companyUserModel) TableName() string { return "company_users" }
