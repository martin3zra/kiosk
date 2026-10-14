// Package kiosk is spark's installable ops console — company/plan/addon
// management, user administration, and impersonation, mounted at /kiosks.
//
// A host app wires it in with a single Register call; kiosk:install (in
// spark's own CLI) copies this module's migrations and React pages into the
// host repo, since neither camel nor Vite can pick them up from inside an
// imported Go module directly.
package kiosk

import (
	"context"
	"strings"

	"github.com/martin3zra/forge/auth"
	"github.com/martin3zra/forge/events"
	"github.com/martin3zra/forge/mailer"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// Options configures a Register call.
type Options struct {
	// PendingApprovals optionally supplies a catalog approval notice for the Kiosk panel.
	PendingApprovals PendingApprovals

	// DB is the host app's playsql connection. Kiosk queries its own
	// tables (companies, plans, addons, company_addons, company_users)
	// and the host's users table directly through it — no adapter
	// interfaces.
	DB *playsql.DB

	// Middleware runs before Kiosk's own allowlist check — it must
	// include whatever establishes an authenticated identity on the
	// request context (e.g. the host's AuthenticatedMiddleware), since
	// Kiosk has nothing to check Allowlist against otherwise.
	Middleware []routing.Middleware

	// Allowlist is the set of lowercased emails permitted into /kiosks,
	// independent of the host app's own role/permission system. Nil or
	// empty locks everyone out — it does not disable the routes.
	Allowlist map[string]bool

	// Mailer sends the invitation email companiesAttachUser/companiesCreate
	// trigger for an email that doesn't match an existing user. Build its
	// Config with a sensible default driver (mailer.Log works with no
	// credentials at all) if the host hasn't set one up — Kiosk has no way
	// to detect or override an unconfigured mailer.Mailer from here.
	Mailer mailer.Mailer

	// AppURL is the host's own base URL (e.g. "https://app.example.com",
	// no trailing slash needed) — used to build the absolute accept-
	// invitation link an email recipient can actually click.
	AppURL string

	// Events, when set, receives kiosk's domain events (CompanyCreated, …)
	// as they occur. Each is dispatched inside the transaction that raised
	// it, so a listener's error rolls that operation back — the place to
	// provision per-company defaults Kiosk knows nothing about (a starter
	// location, seed catalog rows). Build it with events.NewDispatcher, wire
	// listeners with events.On (see events.go); nil disables dispatch.
	Events *events.Dispatcher[*playsql.Tx]

	// Tenancy picks where companies live: Kiosk's own tables (Owned, the
	// zero value and the behavior before this option existed) or the
	// host's tenant table through a mapping (Host). See tenancy.go.
	Tenancy Tenancy

	// HomeURL is where the dashboard sends a developer after they start
	// impersonating someone. Empty means "/home".
	HomeURL string
}

func (o Options) homeURL() string {
	if o.HomeURL == "" {
		return "/home"
	}
	return o.HomeURL
}

// Register mounts Kiosk's routes on router at /kiosks.
//
// In Host mode (Options.Tenancy) the company and member routes read the
// host's own tables, "New company" defers to the host, and Kiosk's own
// invitation routes aren't mounted; plans, addons, users and impersonation
// are the same in both modes. Register panics on an invalid Host mapping.
func Register(router *routing.Router, opts Options) {
	dev := developerCheck{allowlist: opts.Allowlist, isDeveloper: opts.Tenancy.IsDeveloper}
	mws := append(append([]routing.Middleware{}, opts.Middleware...), developerMiddleware(dev), approvalNoticeMiddleware(opts.PendingApprovals))

	if opts.Tenancy.Mode == Host {
		host := newHostKiosk(opts)
		router.GroupPrefix("/kiosks", func(rg *routing.Router) {
			rg.GroupWithMiddleware(mws, func(rg2 *routing.Router) {
				host.register(rg2)
				registerShared(rg2, opts, dev, false)
			})
			rg.GroupWithMiddleware(opts.Middleware, func(rg2 *routing.Router) {
				rg2.DELETE("/impersonate", impersonateStop(opts.DB))
			})
		})
		return
	}

	router.GroupPrefix("/kiosks", func(rg *routing.Router) {
		rg.GroupWithMiddleware(mws, func(rg2 *routing.Router) {
			rg2.GET("", dashboardHandler(opts.DB))

			rg2.GET("/roles", rolesIndex(opts.DB))

			rg2.GET("/companies", companiesIndex(opts.DB))
			rg2.POST("/companies", companiesCreate(opts))
			rg2.GET("/companies/:id", companiesShow(opts.DB))
			rg2.PUT("/companies/:id/status", companiesUpdateStatus(opts.DB))
			rg2.PUT("/companies/:id/plan", companiesUpdatePlan(opts.DB))

			members := NewMemberService(MemberServiceOptions{DB: opts.DB, Mailer: opts.Mailer, AppURL: opts.AppURL})

			rg2.GET("/companies/:id/users", companiesUsersIndex(opts.DB))
			rg2.POST("/companies/:id/users", companiesAttachUser(opts.DB, opts.Mailer, opts.Allowlist, opts.AppURL))
			rg2.DELETE("/companies/:id/users/:userId", companiesDetachUser(opts.DB))

			rg2.GET("/companies/:id/invitations", companiesInvitationsIndex(members))
			rg2.POST("/companies/:id/invitations/:invitationId/resend", companiesInvitationsResend(members))

			rg2.POST("/companies/:id/addons", companiesAttachAddon(opts.DB))
			rg2.DELETE("/companies/:id/addons/:addonId", companiesDetachAddon(opts.DB))

			registerShared(rg2, opts, dev, true)
		})

		// impersonateStop is deliberately NOT behind allowlistMiddleware: by
		// the time it's called, the session identity is the impersonated
		// target, not the developer — allowlistMiddleware would 404 it,
		// making impersonation impossible to stop. impersonateStop enforces
		// its own authorization instead (it only proceeds if
		// kiosk_original_user_id is set, which is only ever true after a
		// successful, allowlist-gated impersonateStart), so it just needs
		// opts.Middleware for an authenticated session, not the allowlist.
		rg.GroupWithMiddleware(opts.Middleware, func(rg2 *routing.Router) {
			rg2.DELETE("/impersonate", impersonateStop(opts.DB))
		})

		// Invitation accept routes are fully public — no middleware at
		// all, not even opts.Middleware. The invited person has no
		// account yet, so there's nothing to authenticate against; the
		// token itself (a real DB row with an expiry) is the only
		// credential. See loadValidInvitation in invitations.go.
		rg.Group(func(rg2 *routing.Router) {
			rg2.GET("/invitations/:token", invitationsShow(opts.DB))
			rg2.POST("/invitations/:token", invitationsAccept(opts.DB))
		})
	})
}

// registerShared mounts the routes that work the same in both modes.
// seedCompany is Owned mode's "set company_id from company_users on
// impersonate"; Host mode leaves picking a tenant to the host.
func registerShared(rg *routing.Router, opts Options, dev developerCheck, seedCompany bool) {
	rg.GET("/plans", plansIndex(opts.DB))
	rg.POST("/plans", plansCreate(opts.DB))
	rg.PUT("/plans/:id", plansUpdate(opts.DB))

	rg.GET("/addons", addonsIndex(opts.DB))
	rg.POST("/addons", addonsCreate(opts.DB))

	rg.GET("/users", usersIndex(opts.DB))
	rg.POST("/users", usersCreate(opts.DB))
	rg.PUT("/users/:id", usersUpdate(opts.DB))

	rg.POST("/impersonate/:id", impersonateStartWith(opts.DB, dev, seedCompany))
}

// dashboardHandler renders Kiosk/Dashboard.tsx: company/plan/user counts and
// a companies table with each company's plan name resolved.
func dashboardHandler(db *playsql.DB) routing.HandlerFunc {
	return func(ctx *routing.Context) {
		reqCtx := ctx.Request.Context()

		var companies []companyModel
		if err := db.Model(&companyModel{}).OrderBy("name", playsql.Asc).Get(reqCtx, &companies); err != nil {
			ctx.Error(err)
			return
		}

		var plans []planModel
		if err := db.Model(&planModel{}).Get(reqCtx, &plans); err != nil {
			ctx.Error(err)
			return
		}
		planNames := make(map[int]string, len(plans))
		for _, p := range plans {
			planNames[p.ID] = p.Name
		}

		userCount, err := db.Model(&kioskUserModel{}).Count(reqCtx)
		if err != nil {
			ctx.Error(err)
			return
		}

		type companyRow struct {
			UUID   string `json:"uuid"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Plan   string `json:"plan"`
		}
		rows := make([]companyRow, len(companies))
		for i, c := range companies {
			plan := "—"
			if c.PlanID != nil {
				if name, ok := planNames[*c.PlanID]; ok {
					plan = name
				}
			}
			rows[i] = companyRow{UUID: c.UUID, Name: c.Name, Status: c.Status, Plan: plan}
		}

		ctx.Render("Kiosk/Dashboard", map[string]any{
			"companies": rows,
			"stats": map[string]any{
				"companies": len(companies),
				"plans":     len(plans),
				"users":     userCount,
			},
		})
	}
}

// allowlistMiddleware restricts access to emails present in allowlist. A
// rejection renders as 404, not 403, so an unlisted authenticated user can't
// tell /kiosks exists at all.
func allowlistMiddleware(allowlist map[string]bool) routing.Middleware {
	return developerMiddleware(developerCheck{allowlist: allowlist})
}

// developerMiddleware lets through only developers: listed in the allowlist
// and, when the host set Tenancy.IsDeveloper, passing that too. Rejections
// are 404s, like a URL that doesn't exist.
func developerMiddleware(dev developerCheck) routing.Middleware {
	return func(next routing.HandlerFunc) routing.HandlerFunc {
		return func(ctx *routing.Context) {
			user, _ := ctx.Request.Context().Value(auth.ContextUserID{}).(map[string]any)
			if !dev.check(user, currentIdentifier(ctx.Request.Context())) {
				notFound(ctx)
				return
			}
			next(ctx)
		}
	}
}

// currentIdentifier reads the logged-in user's identifier value (an email,
// in every known usage) from context. It depends only on forge's
// Authenticatable contract — GetAuthIdentifierName names which field the
// host treats as the login identifier — plus the convention that the
// session-stored user was JSON-marshaled with a matching key, which holds
// for any forge app built the way spark's AuthUser is. No spark-specific
// type is imported here.
func currentIdentifier(ctx context.Context) string {
	user := auth.User(ctx)
	if user == nil {
		return ""
	}
	raw, ok := ctx.Value(auth.ContextUserID{}).(map[string]any)
	if !ok {
		return ""
	}
	v, _ := raw[user.GetAuthIdentifierName()].(string)
	return strings.ToLower(v)
}
