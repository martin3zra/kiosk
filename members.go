package kiosk

import (
	"context"
	"errors"
	"time"

	"github.com/martin3zra/forge/mailer"
	"github.com/martin3zra/playsql"
)

// ErrLastOwner is returned by RemoveMember when the target is the only
// owner a company has — a company always keeps exactly one.
var ErrLastOwner = errors.New("kiosk: cannot remove a company's last owner")

// MemberService is the host-facing API for a company's staff: invitations
// (create / resend / revoke) and memberships (role change / removal). It
// carries NO authorization of its own — Kiosk's /kiosks allowlist and a
// host app's own role system stay the host's concern, so a caller wraps
// these with whatever guard it needs (see spark's shop, which gates them
// with .Can("manage:members") plus its own admin ceiling).
//
// Build one per process from the same DB / Mailer / AppURL a Register call
// receives.
type MemberService struct {
	db     *playsql.DB
	mailer mailer.Mailer
	appURL string
}

// MemberServiceOptions mirrors the invitation-relevant fields of
// kiosk.Options.
type MemberServiceOptions struct {
	DB     *playsql.DB
	Mailer mailer.Mailer
	AppURL string
}

// NewMemberService builds a MemberService. Pass the same DB / Mailer /
// AppURL a Register call gets; a host that hasn't configured a mailer
// should default the Config to mailer.Log (Kiosk can't detect a zero-value
// one — see sendInvitationEmail).
func NewMemberService(opts MemberServiceOptions) *MemberService {
	return &MemberService{db: opts.DB, mailer: opts.Mailer, appURL: opts.AppURL}
}

// Invitation is the exported view of a pending invitations row.
type Invitation struct {
	UUID      string
	Name      string
	Email     string
	Role      string
	ExpiresAt time.Time
	// Expired is a convenience — ExpiresAt already in the past. A caller
	// still offers Resend on an expired invitation.
	Expired bool
}

// InviteParams is the input to Invite. RoleID points at a roles.id (the
// host resolves a role name to its id — Kiosk seeds owner/admin, a host
// migration adds the rest; see roles.go).
type InviteParams struct {
	CompanyID int
	RoleID    int
	Name      string
	Email     string
}

// Invite creates — or, for a repeat invite to the same address, refreshes —
// a pending invitation for the company and emails the accept link. The
// invitee accepts through Kiosk's public /kiosks/invitations/:token route
// (mounted by Register), which sets their password and attaches them at
// the invited role.
func (s *MemberService) Invite(ctx context.Context, p InviteParams) (Invitation, error) {
	invite, err := createOrRefreshInvitation(ctx, s.db, p.CompanyID, p.RoleID, p.Name, p.Email)
	if err != nil {
		return Invitation{}, err
	}
	company, roleName, err := s.inviteRefs(ctx, invite)
	if err != nil {
		return Invitation{}, err
	}
	sendInvitationEmail(s.mailer, invite, company.Name, s.appURL)
	return toInvitation(invite, roleName), nil
}

// PendingInvitations lists a company's not-yet-accepted invitations,
// newest first (expired ones included — the caller shows a Resend).
func (s *MemberService) PendingInvitations(ctx context.Context, companyID int) ([]Invitation, error) {
	var rows []invitationModel
	if err := s.db.Model(&invitationModel{}).
		WhereEq("company_id", companyID).WhereNull("accepted_at").
		OrderBy("created_at", playsql.Desc).
		Get(ctx, &rows); err != nil {
		return nil, err
	}

	out := make([]Invitation, 0, len(rows))
	for _, inv := range rows {
		var role roleModel
		if err := s.db.Model(&roleModel{}).Find(ctx, &role, inv.RoleID); err != nil {
			return nil, err
		}
		out = append(out, toInvitation(inv, role.Name))
	}
	return out, nil
}

// Resend regenerates a pending invitation's token/expiry and re-sends the
// email. The uuid must belong to companyID.
func (s *MemberService) Resend(ctx context.Context, companyID int, invitationUUID string) error {
	inv, err := s.pendingInvitation(ctx, companyID, invitationUUID)
	if err != nil {
		return err
	}
	refreshed, err := createOrRefreshInvitation(ctx, s.db, inv.CompanyID, inv.RoleID, inv.Name, inv.Email)
	if err != nil {
		return err
	}
	var company companyModel
	if err := s.db.Model(&companyModel{}).Find(ctx, &company, inv.CompanyID); err != nil {
		return err
	}
	sendInvitationEmail(s.mailer, refreshed, company.Name, s.appURL)
	return nil
}

// Revoke deletes a pending invitation — the invite link stops working. The
// uuid must belong to companyID.
func (s *MemberService) Revoke(ctx context.Context, companyID int, invitationUUID string) error {
	inv, err := s.pendingInvitation(ctx, companyID, invitationUUID)
	if err != nil {
		return err
	}
	_, err = s.db.Model(&invitationModel{}).WhereEq("id", inv.ID).ForceDelete(ctx)
	return err
}

// SetMemberRole changes an existing member's role within a company.
func (s *MemberService) SetMemberRole(ctx context.Context, companyID, userID, roleID int) error {
	membership, err := s.membership(ctx, companyID, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Model(&companyUserModel{}).WhereEq("id", membership.ID).
		Update(ctx, map[string]any{"role_id": roleID})
	return err
}

// RemoveMember detaches a user from a company. It refuses (ErrLastOwner) to
// remove the company's only owner.
func (s *MemberService) RemoveMember(ctx context.Context, companyID, userID int) error {
	membership, err := s.membership(ctx, companyID, userID)
	if err != nil {
		return err
	}

	if membership.RoleID != nil {
		var role roleModel
		if err := s.db.Model(&roleModel{}).Find(ctx, &role, *membership.RoleID); err != nil {
			return err
		}
		if role.Name == "owner" {
			owners, err := s.db.Model(&companyUserModel{}).
				WhereEq("company_id", companyID).WhereEq("role_id", *membership.RoleID).
				Count(ctx)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
	}

	_, err = s.db.Model(&companyUserModel{}).
		WhereEq("company_id", companyID).WhereEq("user_id", userID).
		ForceDelete(ctx)
	return err
}

// --- helpers ---

func (s *MemberService) pendingInvitation(ctx context.Context, companyID int, uuid string) (invitationModel, error) {
	var inv invitationModel
	err := s.db.Model(&invitationModel{}).
		WhereEq("uuid", uuid).WhereEq("company_id", companyID).WhereNull("accepted_at").
		First(ctx, &inv)
	return inv, err
}

func (s *MemberService) membership(ctx context.Context, companyID, userID int) (companyUserModel, error) {
	var m companyUserModel
	err := s.db.Model(&companyUserModel{}).
		WhereEq("company_id", companyID).WhereEq("user_id", userID).
		First(ctx, &m)
	return m, err
}

func (s *MemberService) inviteRefs(ctx context.Context, inv invitationModel) (companyModel, string, error) {
	var company companyModel
	if err := s.db.Model(&companyModel{}).Find(ctx, &company, inv.CompanyID); err != nil {
		return companyModel{}, "", err
	}
	var role roleModel
	if err := s.db.Model(&roleModel{}).Find(ctx, &role, inv.RoleID); err != nil {
		return companyModel{}, "", err
	}
	return company, role.Name, nil
}

func toInvitation(inv invitationModel, roleName string) Invitation {
	out := Invitation{
		UUID:  inv.UUID,
		Name:  inv.Name,
		Email: inv.Email,
		Role:  roleName,
	}
	if inv.ExpiresAt != nil {
		out.ExpiresAt = *inv.ExpiresAt
		out.Expired = time.Now().After(*inv.ExpiresAt)
	} else {
		out.Expired = true
	}
	return out
}
