package kiosk

import (
	"context"
	"errors"
	"testing"

	"github.com/martin3zra/playsql"
)

type memoryMemberships struct {
	rows   map[int64]Membership
	fail   error
	locked bool
}

// A PlaySQL adapter over legacy pivot storage demonstrates that the new service
// does not require its own schema or take ownership of transaction lifecycle.
type pivotStore struct{ tx *playsql.Tx }

func (p pivotStore) Lock(context.Context, int64) error { return nil } // SQLite test has a single writer.
func (p pivotStore) Find(ctx context.Context, c, u int64) (Membership, error) {
	var row companyUserModel
	err := p.tx.Model(&companyUserModel{}).WhereEq("company_id", c).WhereEq("user_id", u).First(ctx, &row)
	if errors.Is(err, playsql.ErrNotFound) {
		return Membership{}, ErrMembershipNotFound
	}
	if err != nil {
		return Membership{}, err
	}
	var role roleModel
	if row.RoleID == nil {
		return Membership{}, ErrMembershipNotFound
	}
	if err = p.tx.Model(&roleModel{}).WhereEq("id", *row.RoleID).First(ctx, &role); err != nil {
		return Membership{}, err
	}
	return Membership{c, u, role.Name, "active"}, nil
}
func (p pivotStore) List(ctx context.Context, c int64) ([]Membership, error) {
	var rows []companyUserModel
	if err := p.tx.Model(&companyUserModel{}).WhereEq("company_id", c).Get(ctx, &rows); err != nil {
		return nil, err
	}
	result := []Membership{}
	for _, r := range rows {
		m, err := p.Find(ctx, c, int64(r.UserID))
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, nil
}
func (p pivotStore) Insert(ctx context.Context, m Membership) error {
	return errors.New("test adapter: insertion unsupported")
}
func (p pivotStore) Save(ctx context.Context, m Membership) error {
	var role roleModel
	if err := p.tx.Model(&roleModel{}).WhereEq("name", m.Role).First(ctx, &role); err != nil {
		return err
	}
	_, err := p.tx.Model(&companyUserModel{}).WhereEq("company_id", m.CompanyID).WhereEq("user_id", m.UserID).Update(ctx, map[string]any{"role_id": role.ID})
	return err
}
func TestMembershipCallerTransactionRollback(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	for _, q := range []string{"INSERT INTO companies(id,uuid,name,status) VALUES(1,'tx','Tx','active')", "INSERT INTO users(id,uuid,name,email,password,role) VALUES(1,'one','One','one@test','hash','user'),(2,'two','Two','two@test','hash','user')", "INSERT INTO company_users(company_id,user_id,role_id) VALUES(1,1,1),(1,2,2)"} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	abort := errors.New("later application failure")
	err := db.Tx(ctx, func(tx *playsql.Tx) error {
		s, err := NewMembershipService(pivotStore{tx}, membershipPolicy())
		if err != nil {
			return err
		}
		if err = s.Put(ctx, 1, Membership{1, 2, "owner", "active"}); err != nil {
			return err
		}
		m, err := s.Authorize(ctx, 1, 2, "read")
		if err != nil {
			return err
		}
		if m.Role != "owner" {
			t.Fatal("adapter write not visible inside transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	var row companyUserModel
	if err = db.Model(&companyUserModel{}).WhereEq("user_id", 2).First(ctx, &row); err != nil || row.RoleID == nil || *row.RoleID != 2 {
		t.Fatal("caller rollback lost")
	}
}

func (m *memoryMemberships) Lock(context.Context, int64) error { m.locked = true; return m.fail }
func (m *memoryMemberships) Find(_ context.Context, c, u int64) (Membership, error) {
	if m.fail != nil {
		return Membership{}, m.fail
	}
	row, ok := m.rows[u]
	if !ok || row.CompanyID != c {
		return Membership{}, ErrMembershipNotFound
	}
	return row, nil
}
func (m *memoryMemberships) List(context.Context, int64) ([]Membership, error) {
	rows := []Membership{}
	for _, r := range m.rows {
		rows = append(rows, r)
	}
	return rows, m.fail
}
func (m *memoryMemberships) Insert(_ context.Context, r Membership) error {
	if !m.locked {
		panic("unlocked insert")
	}
	if _, ok := m.rows[r.UserID]; ok {
		return errors.New("duplicate")
	}
	m.rows[r.UserID] = r
	return m.fail
}
func (m *memoryMemberships) Save(_ context.Context, r Membership) error {
	if !m.locked {
		panic("unlocked save")
	}
	m.rows[r.UserID] = r
	return m.fail
}
func membershipPolicy() MembershipPolicy {
	return MembershipPolicy{ActiveStatus: "active", Statuses: []string{"active", "revoked", "pending"}, Transitions: map[string][]string{"active": {"revoked"}, "revoked": {"active"}}, Roles: map[string][]string{"owner": {"read", "write", "manage:owner", "manage:admin", "manage:member", "manage:viewer"}, "admin": {"read", "write", "manage:member", "manage:viewer"}, "member": {"read"}, "viewer": {"read"}}, ProtectedRole: "owner"}
}
func TestMembershipLifecycle(t *testing.T) {
	store := &memoryMemberships{rows: map[int64]Membership{1: {1, 1, "owner", "active"}, 2: {1, 2, "viewer", "active"}}}
	s, err := NewMembershipService(store, membershipPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, role := range []string{"owner", "admin", "member", "viewer"} {
		store.rows[2] = Membership{1, 2, role, "active"}
		m, err := s.Authorize(ctx, 1, 2, "read")
		if err != nil || m.Role != role {
			t.Fatalf("role %s lost", role)
		}
	}
	store.rows[2] = Membership{1, 2, "viewer", "active"}
	if _, err = s.Authorize(ctx, 1, 2, "write"); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("viewer wrote")
	}
	if err = s.Put(ctx, 1, Membership{1, 2, "viewer", "revoked"}); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 2 || store.rows[2].Status != "revoked" {
		t.Fatal("history lost")
	}
	if _, err = s.Authorize(ctx, 1, 2, "read"); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("revoked authorized")
	}
	if err = s.Put(ctx, 2, Membership{1, 2, "owner", "active"}); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("self reactivated")
	}
	if err = s.Put(ctx, 1, Membership{1, 2, "member", "active"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, 1, Membership{1, 1, "admin", "active"}); !errors.Is(err, ErrLastOwner) {
		t.Fatal("last owner demoted")
	}
	if err = s.Put(ctx, 1, Membership{1, 1, "owner", "revoked"}); !errors.Is(err, ErrLastOwner) {
		t.Fatal("last owner revoked")
	}
	if err = s.Put(ctx, 1, Membership{1, 3, "admin", "active"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, 3, Membership{1, 2, "owner", "active"}); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("admin promoted owner")
	}
	if err = s.Put(ctx, 3, Membership{1, 1, "viewer", "active"}); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("admin changed owner")
	}
	if err = s.Put(ctx, 1, Membership{1, 2, "member", "pending"}); !errors.Is(err, ErrMembershipTransition) {
		t.Fatal("unconfigured edge allowed")
	}
	if _, err = s.Authorize(ctx, 2, 1, "read"); !errors.Is(err, ErrMembershipDenied) {
		t.Fatal("foreign company allowed")
	}
	if err = s.Put(ctx, 1, Membership{1, 2, "unknown", "active"}); !errors.Is(err, ErrMembershipTransition) {
		t.Fatal("unknown role allowed")
	}
	store.fail = errors.New("storage unavailable")
	if _, err = s.Authorize(ctx, 1, 1, "read"); !errors.Is(err, store.fail) {
		t.Fatal("read error swallowed")
	}
	if err = s.Put(ctx, 1, Membership{1, 2, "member", "active"}); !errors.Is(err, store.fail) {
		t.Fatal("lock error swallowed")
	}
}
func TestMembershipPolicyExplicitReactivationAndCopy(t *testing.T) {
	p := membershipPolicy()
	delete(p.Transitions, "revoked")
	store := &memoryMemberships{rows: map[int64]Membership{1: {1, 1, "owner", "active"}, 2: {1, 2, "member", "revoked"}}}
	s, err := NewMembershipService(store, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Transitions["revoked"] = []string{"active"}
	p.Roles["member"] = []string{"write"}
	if err = s.Put(t.Context(), 1, Membership{1, 2, "member", "active"}); !errors.Is(err, ErrMembershipTransition) {
		t.Fatal("reactivation default/mutated policy")
	}
	for _, bad := range []MembershipPolicy{{}, {ActiveStatus: "active", Statuses: []string{"active"}, Roles: map[string][]string{"owner": {}}, ProtectedRole: "missing"}} {
		if _, err = NewMembershipService(store, bad); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

// Exercise the exact legacy service Shop uses, beyond unchanged handler tests.
func TestLegacyMemberServiceRoleAndHardRemoval(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	for _, q := range []string{"INSERT INTO companies(id,uuid,name,status) VALUES(1,'legacy','Legacy','active')", "INSERT INTO users(id,uuid,name,email,password,role) VALUES(1,'one','One','one@test','hash','user'),(2,'two','Two','two@test','hash','user')", "INSERT INTO company_users(company_id,user_id,role_id) VALUES(1,1,1),(1,2,2)"} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := NewMemberService(MemberServiceOptions{DB: db})
	if err := s.SetMemberRole(ctx, 1, 2, 1); err != nil {
		t.Fatal(err)
	}
	var m companyUserModel
	if err := db.Model(&companyUserModel{}).WhereEq("user_id", 2).First(ctx, &m); err != nil || m.RoleID == nil || *m.RoleID != 1 {
		t.Fatal("legacy role resolution failed")
	}
	if err := s.RemoveMember(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	n, err := db.Model(&companyUserModel{}).WhereEq("user_id", 2).Count(ctx)
	if err != nil || n != 0 {
		t.Fatal("legacy hard deletion changed")
	}
	if err := s.RemoveMember(ctx, 1, 1); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("legacy owner protection: %v", err)
	}
}
