# Kiosk

Kiosk is an installable ops console for apps built on [forge](https://github.com/martin3zra/forge) and [playsql](https://github.com/martin3zra/playsql) — company/plan/addon management, user administration, and impersonation, mounted at `/kiosks`.

It's designed to be dropped into a project only when that project actually needs it. Not every forge app is a multi-tenant SaaS product; Kiosk stays out of the ones that aren't, at zero cost, until you run one install command.

- **A real Go dependency, not copied source.** `go get` it like any module. Its Go logic updates the normal way (`go get -u`), independent of your app.
- **Migrations and pages are copied into your project**, not left inside the module. Neither `camel` (the migration runner) nor Vite can see inside an imported Go module, so `kiosk:install` copies them out — the same reason [shadcn/ui](https://ui.shadcn.com)'s CLI copies component source into your repo instead of shipping it as an npm package. Once copied, they're yours: edit them, and Kiosk won't silently overwrite your changes.
- **Access is a separate allowlist**, not tied to your app's own roles/permissions. Works the same way regardless of what auth system the host app has.

## Install

```sh
go get github.com/martin3zra/kiosk
./cli kiosk:install
```

`kiosk:install` copies Kiosk's migrations and React pages into your project (`database/`, `resources/js/Pages/Kiosk/`, `resources/js/layouts/`), and prints the two things it deliberately does *not* do for you:

```
Kiosk v0.1.0 installed — 7 file(s) tracked in .kiosk-lock.

Next steps:
  1. Add to app/routes.go:
       import "github.com/martin3zra/kiosk"
       kiosk.Register(s.route, kiosk.Options{ DB: s.play, Middleware: []routing.Middleware{s.AuthenticatedMiddleware}, Allowlist: s.config.kiosk.developers })
  2. Add KIOSK_DEVELOPERS to .env
  3. Run: camel migrate && npm run build
```

It only ever writes files it owns (tracked in `.kiosk-lock`) — never `routes.go`, never `.env`. Wiring the two lines above into your app is a deliberate manual step, on purpose: editing hand-written app source programmatically is a much easier way to break something than copying in fresh files.

### Wiring it in

```go
// app/routes.go
import "github.com/martin3zra/kiosk"

kiosk.Register(s.route, kiosk.Options{
	DB:         s.play,
	Middleware: []routing.Middleware{s.AuthenticatedMiddleware},
	Allowlist:  s.config.kiosk.developers,
})
```

```go
// app/config.go
type Kiosk struct {
	developers map[string]bool // lowercased email -> true
}

// in LoadConfig():
kiosk: Kiosk{
	developers: parseAllowlist(os.Getenv("KIOSK_DEVELOPERS")),
},

func parseAllowlist(raw string) map[string]bool {
	out := map[string]bool{}
	for _, email := range strings.Split(raw, ",") {
		email = strings.ToLower(strings.TrimSpace(email))
		if email != "" {
			out[email] = true
		}
	}
	return out
}
```

```sh
# .env
KIOSK_DEVELOPERS=you@example.com,teammate@example.com
```

Then:

```sh
camel migrate
npm run build
```

## Options

```go
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
```

An empty `Allowlist` locks everyone out but the routes still exist (they 404 per request, not because they're unregistered) — that's the "installed but nobody's in it yet" state. A project with Kiosk never installed has no routes at all, because `kiosk.Register` is never called.

Rejections render as **404, not 403** — an unlisted authenticated user can't tell `/kiosks` exists.

## What it exposes

| Method | Path | Purpose |
|---|---|---|
| GET | `/kiosks` | Dashboard — company/plan/user counts, companies table |
| GET | `/kiosks/companies` | List companies |
| POST | `/kiosks/companies` | Create a company |
| GET | `/kiosks/companies/:id` | Company detail |
| PUT | `/kiosks/companies/:id/status` | Set status (`active`/`suspended`) |
| PUT | `/kiosks/companies/:id/plan` | Change plan |
| GET | `/kiosks/plans` | List plans |
| POST | `/kiosks/plans` | Create a plan |
| PUT | `/kiosks/plans/:id` | Update a plan |
| GET | `/kiosks/addons` | List addons |
| POST | `/kiosks/addons` | Create an addon |
| POST | `/kiosks/companies/:id/addons` | Attach an addon to a company |
| DELETE | `/kiosks/companies/:id/addons/:addonId` | Detach an addon |
| GET | `/kiosks/users` | List users, all companies |
| POST | `/kiosks/users` | Create a user |
| PUT | `/kiosks/users/:id` | Update a user |
| POST | `/kiosks/impersonate/:id` | Start impersonating a user |
| DELETE | `/kiosks/impersonate` | Stop impersonating |

Money fields (`price_cents` on plans and addons) are integer cents, not decimals — matching the convention already implied by forge's `foundation.MoneyToText(cents int64, currency string)`.

### Creating a company

```http
POST /kiosks/companies
Content-Type: application/json

{"name": "Acme Inc", "plan_id": 1}
```

New companies default to `status: "active"`.

### Addon scheduling

`active_from`/`active_until` are optional RFC 3339 timestamps. Validation lives in the request form itself, not the handler — `PrepareForValidation` parses the raw strings before field rules run, `PassedValidation` checks that the window isn't inverted after they pass:

```http
POST /kiosks/addons
Content-Type: application/json

{
  "name": "Extra seats",
  "price_cents": 500,
  "currency": "USD",
  "active_from": "2026-01-01T00:00:00Z",
  "active_until": "2026-06-01T00:00:00Z"
}
```

### Creating a user

```http
POST /kiosks/users
Content-Type: application/json

{"name": "Grace Hopper", "email": "grace@example.com", "role": "admin"}
```

New users get a random password and `must_change_password: true` — there's no invite/email flow yet, so an admin resets the password out of band, or wires one up on top.

### Impersonation

```http
POST /kiosks/impersonate/42
```

Swaps the current session to user 42, stashing the original id so `DELETE /kiosks/impersonate` can restore it. Session-only for v1 — no audit table. A developer can't impersonate another allow-listed developer, and impersonation sessions don't stack (start a second one without stopping the first and you get `409 Conflict`).

If you're embedding Kiosk in another forge app: the session value it writes on impersonate (and expects your app's own auth layer to read back) is a `map[string]any` keyed by your `Authenticatable`'s JSON field names — not a Go struct. Sessions round-trip through a DB-backed store as JSON between requests, so this is the shape your app's own session-to-identity decoder needs to already handle.

## Data model

Kiosk owns five tables, migrated via `camel`:

```
users (host app's own table, untouched)
  id, uuid, name, email, role, ...
       │
       │ company_users (pivot — role here is company-membership role,
       │                 distinct from the host's users.role)
       ▼
companies                          plans
  id, uuid, name, status,  ──▶       id, uuid, name, price_cents,
  plan_id, deleted_at                currency, limits (json)

companies ──▶ company_addons ◀── addons
  (pivot)
```

`companies` is soft-deleted (`deleted_at`); the rest are hard-deleted.

## Updating

```sh
./cli kiosk:update            # latest
./cli kiosk:update v0.2.0     # pinned version
```

This runs `go get github.com/martin3zra/kiosk@<version>` — deliberately **not** `go get -u`, which would also upgrade every other already-required module (forge, playsql, ...) as a side effect. It then re-runs `kiosk:install` in a fresh process (Go resolves which version of an imported package's code runs at compile time, so the update itself has to land before the install step can see it) to pick up any migrations or pages that changed in the new version.

Re-running install is diff-aware, via `.kiosk-lock` (plain JSON: path, kind, sha256, the Kiosk version that wrote it):

| On-disk file | New version | Result |
|---|---|---|
| matches the lock hash (untouched) | changed | updated automatically |
| matches the lock hash | unchanged | left alone |
| doesn't match the lock hash (you edited it) | changed or not | **prompted** — overwrite or keep |
| not in the lock at all | — | **prompted** — a new file this version adds |

## Uninstalling

```sh
./cli kiosk:uninstall
```

Two things it checks before deleting anything — real output, not paraphrased:

```
  ! resources/js/Pages/Kiosk/Dashboard.tsx has local changes kiosk:install didn't make.
    Delete it anyway? [y/N]
  ⚠ companies: 12 row(s) — not deleted, will be orphaned
  ⚠ plans: 4 row(s) — not deleted, will be orphaned

Continue uninstalling Kiosk's code? [y/N]
```

Row counts come from a live query against each table it's about to orphan, not a generic warning. Uninstall only ever removes the files it copied in — dropping the tables themselves (`camel rollback`, or a hand-written migration) is left to you, on purpose, since that's a data-loss decision Kiosk shouldn't make on your behalf.

After it finishes, remove the `kiosk` import and `kiosk.Register(...)` call from `routes.go`, remove `KIOSK_DEVELOPERS` from `.env`, and run `go mod tidy`.

## Design notes

- **No app-specific dependency, in either direction.** Kiosk queries the host's `users` table directly via `playsql` (it knows the shape it expects — `id`, `uuid`, `name`, `email`, `role`, `email_verified_at`, `must_change_password`, timestamps) rather than through an adapter interface. It also can't import anything from the host app, so it can't assume a concrete user type either — see the impersonation note above.
- **`currentIdentifier`** (the allowlist check) depends only on `forge`'s `Authenticatable` contract — `GetAuthIdentifierName()` names which session field is the login identifier — plus the convention that the session-stored user was JSON-marshaled with a matching key. No forge app needs anything beyond what it already has for login to make this work.
- **Rejections are 404, not 403.** An unlisted authenticated user gets exactly the same response as a URL that doesn't exist.

## Testing

```sh
go test ./...
```

Handler tests exercise real HTTP request/response cycles via `httptest` against an in-memory sqlite database (schema matches what `camel` would create from `migrations/*.yaml` — verified against that YAML with `camel plan` while building it, not just assumed). Install/uninstall tests run against real temp directories on disk, covering fresh installs, idempotent re-runs, adopting pre-existing byte-identical files, conflicting with untracked files, and detecting local edits.
