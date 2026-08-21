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
			rg2.GET("", dashboardHandler)

			rg2.GET("/companies", companiesIndex(opts.DB))
			rg2.POST("/companies", companiesCreate(opts.DB))
			rg2.GET("/companies/:id", companiesShow(opts.DB))
			rg2.PUT("/companies/:id/status", companiesUpdateStatus(opts.DB))
			rg2.PUT("/companies/:id/plan", companiesUpdatePlan(opts.DB))

			rg2.GET("/plans", plansIndex(opts.DB))
			rg2.POST("/plans", plansCreate(opts.DB))
			rg2.PUT("/plans/:id", plansUpdate(opts.DB))

			rg2.GET("/addons", addonsIndex(opts.DB))
			rg2.POST("/addons", addonsCreate(opts.DB))
			rg2.POST("/companies/:id/addons", companiesAttachAddon(opts.DB))
			rg2.DELETE("/companies/:id/addons/:addonId", companiesDetachAddon(opts.DB))
		})
	})
}

// dashboardHandler is a placeholder — proves the routing/middleware/allowlist
// chain end to end before the real dashboard (and its React page) exist.
func dashboardHandler(ctx *routing.Context) {
	ctx.JSON(http.StatusOK, map[string]any{
		"ok":      true,
		"message": "kiosk is wired",
	})
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
