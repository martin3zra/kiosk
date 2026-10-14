package kiosk

import (
	"testing"
	"time"

	"github.com/martin3zra/forge/session"
)

func TestEndImpersonationOnLogout(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			db := testDB(t)
			sess, req := newTestSession(t, db)
			var current, other impersonationSessionModel
			for _, row := range []*impersonationSessionModel{&current, &other} {
				if err := db.Model(row).Create(t.Context(), row, map[string]any{
					"developer_user_id": 1, "target_user_id": 2, "started_at": time.Now(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			if legacy {
				if _, err := db.Model(&impersonationSessionModel{}).WhereEq("id", other.ID).Update(t.Context(), map[string]any{"target_user_id": 3}); err != nil {
					t.Fatal(err)
				}
			} else {
				sess.Put(impersonateAuditSessionKey, float64(current.ID))
			}
			sess.Put(ImpersonateSessionKey, float64(1))
			sess.Put("user_id", float64(2))
			sess.Put("company_id", 99)
			if err := EndImpersonation(req, db); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{ImpersonateSessionKey, impersonateAuditSessionKey, "company_id"} {
				if sess.Get(key) != nil {
					t.Fatalf("%s was not cleared", key)
				}
			}
			if sess.Get("user_id") != float64(2) {
				t.Fatal("logout cleanup must not restore the developer")
			}
			if err := db.Model(&current).Find(t.Context(), &current, current.ID); err != nil {
				t.Fatal(err)
			}
			if current.EndedAt == nil {
				t.Fatal("audit row left open")
			}
			if err := db.Model(&other).Find(t.Context(), &other, other.ID); err != nil {
				t.Fatal(err)
			}
			if other.EndedAt != nil {
				t.Fatal("another session was ended")
			}
			if err := EndImpersonation(req, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(t.Context(), "CREATE TABLE sessions (id TEXT PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			mgr := session.NewSessionManager(session.NewDatabaseStore(db), time.Hour, time.Hour, time.Hour, "test_session", "", false, false)
			if err := mgr.Invalidate(req); err != nil {
				t.Fatal(err)
			}
			if sess.Get("user") != nil || sess.Get("user_id") != float64(0) {
				t.Fatal("logout left an authenticated identity")
			}
			if sess.Get(ImpersonateSessionKey) != nil {
				t.Fatal("logout left impersonation active")
			}
		})
	}
}

func TestEndImpersonationFailurePreservesMarkers(t *testing.T) {
	db := testDB(t)
	sess, req := newTestSession(t, db)
	sess.Put(ImpersonateSessionKey, 1)
	sess.Put("user_id", 2)
	sess.Put("company_id", 99)
	if _, err := db.Exec(t.Context(), "DROP TABLE impersonation_sessions"); err != nil {
		t.Fatal(err)
	}
	if err := EndImpersonation(req, db); err == nil {
		t.Fatal("expected database failure")
	}
	if sess.Get(ImpersonateSessionKey) != 1 || sess.Get("company_id") != 99 {
		t.Fatal("failed cleanup changed markers")
	}
}
