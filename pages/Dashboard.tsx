import { router } from "@inertiajs/react";
import { type FormEvent, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import KioskLayout from "@/layouts/kiosk-layout";
import { useCsrfHeaders } from "@/lib/csrf";

interface CompanyRow {
  uuid: string;
  name: string;
  status: string;
  plan: string;
}

interface Member {
  uuid: string;
  name: string;
  email: string;
  role: string;
}

// rolesIndex returns the raw roleModel rows — no custom json tags, same
// convention companiesIndex/plansIndex/usersIndex already use (capitalized
// Go field names as JSON keys), unlike memberRow/CompanyRow below, which are
// purpose-built response shapes with explicit lowercase tags.
interface Role {
  UUID: string;
  Name: string;
  Description: string | null;
}

interface DashboardProps {
  stats: { companies: number; plans: number; users: number };
  companies: CompanyRow[];
}

// Kiosk's endpoints are a plain JSON API, not Inertia-rendered responses —
// every mutation here goes through fetch() + useCsrfHeaders(), not
// useForm()/router.post(), which expect an Inertia-formatted response.
//
// Every fetch() call needs Accept: application/json, not just Content-Type
// — forge's WithRequest checks Accept to decide whether a validation
// failure gets a clean JSON error response or a session-flash fallback that
// panics outside a browser-form submission (see roles.go's package doc /
// the kiosk repo's TestCompaniesCreateRequiresOwner for the full story).
// Centralized here once rather than on every call site below.
export default function Dashboard({ stats, companies }: DashboardProps) {
  const headers = { ...useCsrfHeaders(), Accept: "application/json" };
  const [roles, setRoles] = useState<Role[]>([]);

  useEffect(() => {
    fetch("/kiosks/roles", { headers })
      .then((res) => (res.ok ? res.json() : []))
      .then(setRoles)
      .catch(() => setRoles([]));
    // Fetch once on mount — the role list doesn't change during a visit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <KioskLayout title="Dashboard">
      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Companies" value={stats.companies} />
        <StatCard label="Plans" value={stats.plans} />
        <StatCard label="Users" value={stats.users} />
      </div>

      <NewCompanyForm headers={headers} />

      <div className="mt-6 space-y-3">
        {companies.map((c) => (
          <CompanyCard key={c.uuid} company={c} headers={headers} roles={roles} />
        ))}
        {companies.length === 0 && (
          <p className="text-muted-foreground py-6 text-center text-sm">
            No companies yet.
          </p>
        )}
      </div>
    </KioskLayout>
  );
}

function StatCard({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-md border p-4">
      <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
        {label}
      </div>
      <div className="mt-1 text-2xl font-semibold">{value}</div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  return (
    <span
      className={
        "rounded px-2 py-0.5 text-xs font-medium " +
        (status === "active"
          ? "bg-emerald-500/10 text-emerald-600"
          : "bg-amber-500/10 text-amber-600")
      }
    >
      {status}
    </span>
  );
}

function NewCompanyForm({ headers }: { headers: Record<string, string> }) {
  const [name, setName] = useState("");
  const [identifier, setIdentifier] = useState("");
  const [ownerName, setOwnerName] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    const res = await fetch("/kiosks/companies", {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        identifier: identifier || undefined,
        owner_name: ownerName,
        owner_email: ownerEmail,
      }),
    });
    setSubmitting(false);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setError(body.error ?? "Could not create the company.");
      return;
    }
    setName("");
    setIdentifier("");
    setOwnerName("");
    setOwnerEmail("");
    router.reload({ only: ["companies", "stats"] });
  }

  return (
    <form
      onSubmit={submit}
      className="mt-6 grid gap-3 rounded-md border p-4 sm:grid-cols-4 sm:items-end"
    >
      <div className="grid gap-1 sm:col-span-2">
        <Label htmlFor="company-name">Company name</Label>
        <Input
          id="company-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Acme Inc"
          required
        />
      </div>
      <div className="grid gap-1 sm:col-span-2">
        <Label htmlFor="company-identifier">Identifier (RNC)</Label>
        <Input
          id="company-identifier"
          value={identifier}
          onChange={(e) => setIdentifier(e.target.value)}
          placeholder="1-23-45678-9"
        />
      </div>
      <div className="grid gap-1 sm:col-span-2">
        <Label htmlFor="company-owner-name">Owner name</Label>
        <Input
          id="company-owner-name"
          value={ownerName}
          onChange={(e) => setOwnerName(e.target.value)}
          required
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor="company-owner-email">Owner email</Label>
        <Input
          id="company-owner-email"
          type="email"
          value={ownerEmail}
          onChange={(e) => setOwnerEmail(e.target.value)}
          required
        />
      </div>
      <Button type="submit" disabled={submitting}>
        {submitting ? "Creating…" : "Create company"}
      </Button>
      {error && (
        <p className="text-destructive text-sm sm:col-span-4">{error}</p>
      )}
    </form>
  );
}

function CompanyCard({
  company,
  headers,
  roles,
}: {
  company: CompanyRow;
  headers: Record<string, string>;
  roles: Role[];
}) {
  const [expanded, setExpanded] = useState(false);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [loading, setLoading] = useState(false);

  async function loadMembers() {
    setLoading(true);
    const res = await fetch(`/kiosks/companies/${company.uuid}/users`, {
      headers,
    });
    setMembers(res.ok ? await res.json() : []);
    setLoading(false);
  }

  function toggle() {
    const next = !expanded;
    setExpanded(next);
    if (next && members === null) void loadMembers();
  }

  return (
    <div className="rounded-md border">
      <button
        type="button"
        onClick={toggle}
        className="flex w-full items-center justify-between px-4 py-3 text-left"
      >
        <div className="flex items-center gap-3">
          <span className="font-medium">{company.name}</span>
          <StatusBadge status={company.status} />
        </div>
        <span className="text-muted-foreground text-sm">{company.plan}</span>
      </button>

      {expanded && (
        <div className="border-t px-4 py-3">
          {loading && (
            <p className="text-muted-foreground text-sm">Loading…</p>
          )}
          {members && (
            <MembersPanel
              company={company}
              members={members}
              headers={headers}
              roles={roles}
              onChange={loadMembers}
            />
          )}
        </div>
      )}
    </div>
  );
}

function MembersPanel({
  company,
  members,
  headers,
  roles,
  onChange,
}: {
  company: CompanyRow;
  members: Member[];
  headers: Record<string, string>;
  roles: Role[];
  onChange: () => void;
}) {
  return (
    <div className="space-y-4">
      <table className="w-full text-sm">
        <thead className="text-muted-foreground text-left">
          <tr>
            <th className="py-1 font-medium">Name</th>
            <th className="font-medium">Email</th>
            <th className="font-medium">Role</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {members.map((m) => (
            <MemberRow
              key={m.uuid}
              company={company}
              member={m}
              headers={headers}
              onChange={onChange}
            />
          ))}
          {members.length === 0 && (
            <tr>
              <td
                colSpan={4}
                className="text-muted-foreground py-3 text-center"
              >
                No members yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <AssignUserForm
        company={company}
        headers={headers}
        roles={roles}
        onChange={onChange}
      />
    </div>
  );
}

function MemberRow({
  company,
  member,
  headers,
  onChange,
}: {
  company: CompanyRow;
  member: Member;
  headers: Record<string, string>;
  onChange: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function impersonate() {
    setBusy(true);
    setError(null);
    const res = await fetch(`/kiosks/impersonate/${member.uuid}`, {
      method: "POST",
      headers,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setError(body.error ?? "Could not impersonate.");
      setBusy(false);
      return;
    }
    // A hard navigation, not an Inertia visit — the server-side identity
    // just changed underneath this session, so every prop (auth.user,
    // permissions, ...) needs a fresh full-page load, not a client-side
    // partial reload that could keep stale assumptions around.
    window.location.assign("/home");
  }

  async function remove() {
    setBusy(true);
    await fetch(`/kiosks/companies/${company.uuid}/users/${member.uuid}`, {
      method: "DELETE",
      headers,
    });
    setBusy(false);
    onChange();
  }

  return (
    <tr className="border-t align-top">
      <td className="py-2">{member.name}</td>
      <td className="text-muted-foreground">{member.email}</td>
      <td className="text-muted-foreground">{member.role}</td>
      <td className="py-2 text-right">
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            size="xs"
            variant="outline"
            disabled={busy}
            onClick={impersonate}
          >
            Impersonate
          </Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            disabled={busy}
            onClick={remove}
          >
            Remove
          </Button>
        </div>
        {error && <p className="text-destructive mt-1 text-xs">{error}</p>}
      </td>
    </tr>
  );
}

function AssignUserForm({
  company,
  headers,
  roles,
  onChange,
}: {
  company: CompanyRow;
  headers: Record<string, string>;
  roles: Role[];
  onChange: () => void;
}) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    const res = await fetch(`/kiosks/companies/${company.uuid}/users`, {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({ name, email, role: role || undefined }),
    });
    setSubmitting(false);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setError(body.error ?? "Could not assign the user.");
      return;
    }
    setName("");
    setEmail("");
    setRole("");
    onChange();
  }

  return (
    <form onSubmit={submit} className="grid gap-3 sm:grid-cols-4 sm:items-end">
      <div className="grid gap-1">
        <Label htmlFor={`${company.uuid}-name`}>Name</Label>
        <Input
          id={`${company.uuid}-name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor={`${company.uuid}-email`}>Email</Label>
        <Input
          id={`${company.uuid}-email`}
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor={`${company.uuid}-role`}>Role</Label>
        <select
          id={`${company.uuid}-role`}
          value={role}
          onChange={(e) => setRole(e.target.value)}
          className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
        >
          <option value="">Default (admin)</option>
          {roles.map((r) => (
            <option key={r.UUID} value={r.Name}>
              {r.Name}
            </option>
          ))}
        </select>
      </div>
      <Button type="submit" disabled={submitting}>
        {submitting ? "Assigning…" : "Assign user"}
      </Button>
      {error && (
        <p className="text-destructive text-sm sm:col-span-4">{error}</p>
      )}
    </form>
  );
}
