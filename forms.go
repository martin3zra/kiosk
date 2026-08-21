package kiosk

import (
	"errors"
	"fmt"
	"time"

	"github.com/martin3zra/forge/support"
)

type createCompanyForm struct {
	support.FormRequest
	Name   string `json:"name"`
	PlanID *int   `json:"plan_id"`
}

func (f *createCompanyForm) Rules() map[string]any {
	return map[string]any{"name": "required"}
}

type updateCompanyStatusForm struct {
	support.FormRequest
	Status string `json:"status"`
}

func (f *updateCompanyStatusForm) Rules() map[string]any {
	return map[string]any{"status": "required"}
}

type updateCompanyPlanForm struct {
	support.FormRequest
	PlanID int `json:"plan_id"`
}

func (f *updateCompanyPlanForm) Rules() map[string]any {
	return map[string]any{"plan_id": "required"}
}

type planForm struct {
	support.FormRequest
	Name       string  `json:"name"`
	PriceCents int64   `json:"price_cents"`
	Currency   string  `json:"currency"`
	Limits     *string `json:"limits"`
}

func (f *planForm) Rules() map[string]any {
	return map[string]any{"name": "required"}
}

type addonForm struct {
	support.FormRequest
	Name        string  `json:"name"`
	PriceCents  int64   `json:"price_cents"`
	Currency    string  `json:"currency"`
	ActiveFrom  *string `json:"active_from"`
	ActiveUntil *string `json:"active_until"`

	// Parsed by PrepareForValidation, checked by PassedValidation. dateErr is
	// the one thing the handler still inspects — see the comment on
	// PassedValidation for why a FormRequest hook can't reject a request on
	// its own.
	activeFrom  *time.Time
	activeUntil *time.Time
	dateErr     error
}

func (f *addonForm) Rules() map[string]any {
	return map[string]any{"name": "required"}
}

// PrepareForValidation runs before Rules() are evaluated (forge's Validator
// calls it as the beforeValidation callback). It's where the raw
// active_from/active_until strings get parsed, so field-rule evaluation and
// the handler both only ever see the parsed result.
func (f *addonForm) PrepareForValidation() {
	var err error
	if f.activeFrom, err = parseOptionalTime(f.ActiveFrom); err != nil {
		f.dateErr = fmt.Errorf("active_from: %w", err)
		return
	}
	if f.activeUntil, err = parseOptionalTime(f.ActiveUntil); err != nil {
		f.dateErr = fmt.Errorf("active_until: %w", err)
	}
}

// PassedValidation runs after Rules() succeed. It layers on the one check
// that isn't expressible as a field rule — an explicit window can't end
// before it starts.
//
// Note this can't reject the request by itself: support.ParseRequest calls
// PassedValidation for its side effects only and always returns nil
// afterward (see forge's request.go), so dateErr still has to be checked
// once in the handler. Rules()-level failures don't have this problem —
// those go through Errors() and short-circuit before PassedValidation ever
// runs — but active_from/active_until aren't struct fields the built-in
// rule tokens can see (they arrive as JSON strings, not time.Time), so
// there's no rule to attach them to.
func (f *addonForm) PassedValidation() {
	if f.dateErr != nil {
		return
	}
	if f.activeFrom != nil && f.activeUntil != nil && f.activeUntil.Before(*f.activeFrom) {
		f.dateErr = errors.New("active_until must not be before active_from")
	}
}

func parseOptionalTime(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

type attachAddonForm struct {
	support.FormRequest
	AddonID int `json:"addon_id"`
}

func (f *attachAddonForm) Rules() map[string]any {
	return map[string]any{"addon_id": "required"}
}

type createUserForm struct {
	support.FormRequest
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (f *createUserForm) Rules() map[string]any {
	return map[string]any{"name": "required", "email": "required"}
}

type updateUserForm struct {
	support.FormRequest
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (f *updateUserForm) Rules() map[string]any {
	return map[string]any{"name": "required", "email": "required"}
}
