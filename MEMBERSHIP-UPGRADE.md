# Opt-in membership service (v0.8.0)

Existing `Register`, `Tenancy`, Host mappings and `MemberService` require no
changes. Owned removal still hard-deletes; Host still resolves its boolean to
admin/member. Existing role IDs, invitations and one-company attachment remain
unchanged. No migrations, dependency changes or installer changes are included.

Applications with existing membership tables can instead construct
`NewMembershipService(store, MembershipPolicy{...})` inside each operation's
transaction. This service does not mount console routes or retrofit legacy
handlers. Do not mount legacy membership writers over the same adapted tables.

`MembershipStore` supplies Lock, Find, List, Insert and Save. Use the application's
transaction-bound PlaySQL builders for persistence; Kiosk needs no pool, schema,
table names or commit/rollback handle. Find returns `ErrMembershipNotFound` for
inaccessible or missing rows. List retains inactive records. Save changes only
role/status, preserving surrogate IDs or composite keys and all historical rows.

The host authenticates the actor and validates company/user eligibility. Kiosk
reloads the actor, requires the configured active status, and resolves exact
role-to-permission mappings. `Authorize` handles arbitrary application abilities.
`Put` requires `manage:<current-role>` and `manage:<requested-role>`, providing
explicit role ceilings. A viewer can have read permission without write or
management permission. Role identifiers are opaque strings, never booleans.

Configure `Statuses`, `ActiveStatus`, `Transitions`, `Roles`, and optionally
`ProtectedRole`. For example, active -> revoked and revoked -> active explicitly
allow soft revocation and reactivation. Without the latter edge reactivation is
denied. Put creates only active records and never deletes records. Repeating an
identical state is allowed. Revoked actors cannot reactivate themselves.
ProtectedRole refuses removing/demoting/revoking its last active holder with
`ErrLastOwner`. No owner transfer shortcut or implicit elevated actor exists.

```go
// tx belongs to the application. store wraps tx and cannot outlive it.
service, err := kiosk.NewMembershipService(store, policy)
if err != nil { return err }
if err := service.Put(ctx, authenticatedUserID, kiosk.Membership{
    CompanyID: companyID, UserID: memberID, Role: "viewer", Status: "revoked",
}); err != nil { return err } // caller rolls back
// Only caller decides commit or rollback, including later business failures.
```

Every writer must serialize through Store.Lock for the company, e.g. locking its
parent row before actor/target reads. Keep the lock until the caller's transaction
ends, including absent-row inserts. Use an isolation level that sees prior
committed writers after locking (PostgreSQL READ COMMITTED), or handle transaction
serialization conflicts. No adapter may elevate database privileges, evade RLS,
open a different transaction/pool, or hide storage errors. The service propagates
storage errors and never commits partial changes. A read-only runtime role remains
read-only; management needs an independently authorized handle, not extra grants
implied by this API. Context cancellation belongs to the caller's transaction.

Upgrade: keep legacy consumers unchanged; implement/test one adapter for existing
tables, configure exact roles/statuses, then route intended reads/writes through
this service. Preserve session/company selection and database isolation in the
host. Rollback is reverting application wiring; no data conversion is required.
Do not use the legacy hard-delete API after adopting retained membership history.
