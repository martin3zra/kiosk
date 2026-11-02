package kiosk

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrMembershipDenied     = errors.New("kiosk: membership permission denied")
	ErrMembershipNotFound   = errors.New("kiosk: membership not found")
	ErrMembershipTransition = errors.New("kiosk: membership transition denied")
)

// Membership preserves application role identifiers and composite membership identity.
type Membership struct {
	CompanyID int64  `db:"company_id"`
	UserID    int64  `db:"user_id"`
	Role      string `db:"role"`
	Status    string `db:"status"`
}

// MembershipStore is bound to ONE caller-owned operation transaction. Lock must
// serialize all membership writers for the company until that transaction ends
// (including creation of previously absent memberships). Find returns
// ErrMembershipNotFound for missing or inaccessible identities/companies.
// List includes inactive history. Insert must reject an existing composite key;
// Save updates role/status only, preserving identifiers. No method may commit,
// rollback, acquire a pool, elevate privileges, or bypass RLS. All writers must
// follow the same locking protocol. The caller rolls back on any service error.
type MembershipStore interface {
	Lock(context.Context, int64) error
	Find(context.Context, int64, int64) (Membership, error)
	List(context.Context, int64) ([]Membership, error)
	Insert(context.Context, Membership) error
	Save(context.Context, Membership) error
}

// MembershipPolicy is explicit opt-in configuration. Only ActiveStatus grants
// permissions. Transitions enumerate every permitted status change, including
// reactivation; omitted edges deny. ProtectedRole optionally retains the last
// active holder. Role permissions are exact strings; there is no boolean role.
type MembershipPolicy struct {
	ActiveStatus  string
	Statuses      []string
	Transitions   map[string][]string
	Roles         map[string][]string
	ProtectedRole string
}

// MembershipService is independent of Register and legacy MemberService.
// It owns policy; the adapter owns persistence and the caller owns identity,
// company eligibility, transaction lifecycle and database privileges.
type MembershipService struct {
	policy MembershipPolicy
	store  MembershipStore
}

func NewMembershipService(store MembershipStore, p MembershipPolicy) (*MembershipService, error) {
	if store == nil || p.ActiveStatus == "" || !contains(p.Statuses, p.ActiveStatus) || len(p.Roles) == 0 {
		return nil, fmt.Errorf("kiosk: invalid membership policy/store")
	}
	for role := range p.Roles {
		if role == "" {
			return nil, fmt.Errorf("kiosk: empty membership role")
		}
	}
	if p.ProtectedRole != "" {
		if _, ok := p.Roles[p.ProtectedRole]; !ok {
			return nil, fmt.Errorf("kiosk: unknown protected role")
		}
	}
	for from, tos := range p.Transitions {
		if !contains(p.Statuses, from) {
			return nil, fmt.Errorf("kiosk: unknown source status")
		}
		for _, to := range tos {
			if !contains(p.Statuses, to) {
				return nil, fmt.Errorf("kiosk: unknown target status")
			}
		}
	}
	// Freeze caller configuration, so mutation after construction cannot alter authorization.
	cp := p
	cp.Statuses = append([]string(nil), p.Statuses...)
	cp.Roles = map[string][]string{}
	cp.Transitions = map[string][]string{}
	for k, v := range p.Roles {
		cp.Roles[k] = append([]string(nil), v...)
	}
	for k, v := range p.Transitions {
		cp.Transitions[k] = append([]string(nil), v...)
	}
	return &MembershipService{policy: cp, store: store}, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Authorize always reloads the actor from the supplied transaction store.
func (s *MembershipService) Authorize(ctx context.Context, company, user int64, permission string) (Membership, error) {
	if company <= 0 || user <= 0 || permission == "" {
		return Membership{}, ErrMembershipDenied
	}
	m, err := s.store.Find(ctx, company, user)
	if errors.Is(err, ErrMembershipNotFound) {
		return Membership{}, ErrMembershipDenied
	}
	if err != nil {
		return Membership{}, err
	}
	if m.CompanyID != company || m.UserID != user || m.Status != s.policy.ActiveStatus || !contains(s.policy.Roles[m.Role], permission) {
		return Membership{}, ErrMembershipDenied
	}
	return m, nil
}

// Put creates an active membership or explicitly changes an existing role/status.
// Existing history is never deleted. Both current and requested roles require
// manage:<role> permission; this supports role ceilings without built-in names.
// Missing rows may only be created active. Reactivation requires an explicit edge.
func (s *MembershipService) Put(ctx context.Context, actor int64, next Membership) error {
	if next.CompanyID <= 0 || next.UserID <= 0 {
		return ErrMembershipDenied
	}
	if _, ok := s.policy.Roles[next.Role]; !ok || !contains(s.policy.Statuses, next.Status) {
		return ErrMembershipTransition
	}
	if err := s.store.Lock(ctx, next.CompanyID); err != nil {
		return err
	}
	if _, err := s.Authorize(ctx, next.CompanyID, actor, "manage:"+next.Role); err != nil {
		return err
	}
	old, err := s.store.Find(ctx, next.CompanyID, next.UserID)
	if errors.Is(err, ErrMembershipNotFound) {
		if next.Status != s.policy.ActiveStatus {
			return ErrMembershipTransition
		}
		return s.store.Insert(ctx, next)
	}
	if err != nil {
		return err
	}
	if old.CompanyID != next.CompanyID || old.UserID != next.UserID {
		return ErrMembershipDenied
	}
	if _, err = s.Authorize(ctx, next.CompanyID, actor, "manage:"+old.Role); err != nil {
		return err
	}
	if old.Status != next.Status && !contains(s.policy.Transitions[old.Status], next.Status) {
		return ErrMembershipTransition
	}
	if old.Role == s.policy.ProtectedRole && old.Status == s.policy.ActiveStatus && (next.Role != old.Role || next.Status != old.Status) {
		rows, err := s.store.List(ctx, next.CompanyID)
		if err != nil {
			return err
		}
		others := 0
		for _, m := range rows {
			if m.CompanyID == next.CompanyID && m.UserID != old.UserID && m.Role == old.Role && m.Status == s.policy.ActiveStatus {
				others++
			}
		}
		if others == 0 {
			return ErrLastOwner
		}
	}
	return s.store.Save(ctx, next)
}
