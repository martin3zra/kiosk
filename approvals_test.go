package kiosk

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/martin3zra/forge/routing"
	"github.com/romsar/gonertia/v3"
)

func TestApprovalNotice(t *testing.T) {
	for _, count := range []int{0, 3} {
		req := httptest.NewRequest("GET", "/kiosks", nil)
		ctx := &routing.Context{Request: req, Response: httptest.NewRecorder()}
		called := false
		approvalNoticeMiddleware(func(context.Context) (PendingApprovalNotice, error) {
			return PendingApprovalNotice{Count: count, URL: "/platform/catalog", Label: "Catálogo pendiente"}, nil
		})(func(ctx *routing.Context) {
			called = true
			value := gonertia.PropsFromContext(ctx.Request.Context())["kioskPendingApprovals"]
			if count == 0 {
				if value != nil {
					t.Fatal("empty queue produced a notice")
				}
			} else {
				notice, ok := value.(PendingApprovalNotice)
				if !ok || notice.Count != count || notice.URL != "/platform/catalog" {
					t.Fatalf("wrong notice: %#v", value)
				}
			}
		})(ctx)
		if !called {
			t.Fatal("page handler not called")
		}
	}
}
