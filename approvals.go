package kiosk

import (
	"context"

	"github.com/martin3zra/forge/routing"
	"github.com/romsar/gonertia/v3"
)

// PendingApprovalNotice is an application-owned approval queue displayed in
// Kiosk. URL must be a trusted application route, not user-supplied input.
type PendingApprovalNotice struct {
	Count int    `json:"count"`
	URL   string `json:"url"`
	Label string `json:"label"`
}

// PendingApprovals reads an application's pending catalog approvals. Kiosk
// calls it only after developer authorization, in both tenancy modes.
type PendingApprovals func(context.Context) (PendingApprovalNotice, error)

func approvalNoticeMiddleware(pending PendingApprovals) routing.Middleware {
	return func(next routing.HandlerFunc) routing.HandlerFunc {
		return func(ctx *routing.Context) {
			if pending != nil {
				notice, err := pending(ctx.Request.Context())
				if err != nil {
					ctx.Error(err)
					return
				}
				if notice.Count > 0 {
					ctx.Request = ctx.Request.WithContext(gonertia.SetProp(ctx.Request.Context(), "kioskPendingApprovals", notice))
				}
			}
			next(ctx)
		}
	}
}
