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
	"net/http"
	"strings"

	"github.com/martin3zra/forge/auth"
	"github.com/martin3zra/forge/foundation"
	"github.com/martin3zra/forge/routing"
	"github.com/martin3zra/playsql"
)

// Options configures a Register call.
type Options struct {
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
}

// Register mounts Kiosk's routes on router at /kiosks.
func Register(router *routing.Router, opts Options) {
	mws := append(append([]routing.Middleware{}, opts.Middleware...), allowlistMiddleware(opts.Allowlist))

	router.GroupPrefix("/kiosks", func(rg *routing.Router) {
		rg.GroupWithMiddleware(mws, func(rg2 *routing.Router) {
			rg2.GET("", dashboardHandler(opts.DB))

			rg2.GET("/companies", companiesIndex(opts.DB))
			rg2.POST("/companies", companiesCreate(opts.DB))
			rg2.GET("/companies/:id", companiesShow(opts.DB))
			rg2.PUT("/companies/:id/status", companiesUpdateStatus(opts.DB))
			rg2.PUT("/companies/:id/plan", companiesUpdatePlan(opts.DB))

			rg2.GET("/companies/:id/users", companiesUsersIndex(opts.DB))
			rg2.POST("/companies/:id/users", companiesAttachUser(opts.DB, opts.Allowlist))
			rg2.DELETE("/companies/:id/users/:userId", companiesDetachUser(opts.DB))

			rg2.GET("/plans", plansIndex(opts.DB))
			rg2.POST("/plans", plansCreate(opts.DB))
			rg2.PUT("/plans/:id", plansUpdate(opts.DB))

			rg2.GET("/addons", addonsIndex(opts.DB))
			rg2.POST("/addons", addonsCreate(opts.DB))
			rg2.POST("/companies/:id/addons", companiesAttachAddon(opts.DB))
			rg2.DELETE("/companies/:id/addons/:addonId", companiesDetachAddon(opts.DB))

			rg2.GET("/users", usersIndex(opts.DB))
			rg2.POST("/users", usersCreate(opts.DB))
			rg2.PUT("/users/:id", usersUpdate(opts.DB))

			rg2.POST("/impersonate/:id", impersonateStart(opts.DB, opts.Allowlist))
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
	})
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
	return func(next routing.HandlerFunc) routing.HandlerFunc {
		return func(ctx *routing.Context) {
			email := currentIdentifier(ctx.Request.Context())
			if email == "" || !allowlist[email] {
				ctx.Error(foundation.HTTPError{StatusCode: http.StatusNotFound, Message: "Not Found"})
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
