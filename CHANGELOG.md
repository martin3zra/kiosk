# Changelog

## v0.8.0 — 2026-11-02

- Add optional transaction-bound MembershipStore, MembershipPolicy and
  MembershipService for existing membership tables, named permissions, explicit
  status transitions, retained revoked history and serialized last-owner guards.
- Preserve legacy Host/Owned registration and MemberService behavior by default.
- See MEMBERSHIP-UPGRADE.md for ownership boundaries and adapter requirements.

Verification: Go tests, race tests, vet, build and module verification passed.
Committed Shop application tests/build passed in an isolated archive against this
API. No dependency, schema or legacy API changes are included.

Release convention: publish an annotated version tag on the verified origin;
do not move published tags. No GitHub release is required by existing practice.
No LICENSE file or license grant is supplied by this release; licensing remains
unresolved and release authorization does not establish downstream reuse rights.
