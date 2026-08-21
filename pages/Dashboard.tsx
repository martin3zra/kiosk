import KioskLayout from "@/layouts/kiosk-layout";

interface CompanyRow {
  id: number;
  name: string;
  status: string;
  plan: string;
}

interface DashboardProps {
  stats: { companies: number; plans: number; users: number };
  companies: CompanyRow[];
}

export default function Dashboard({ stats, companies }: DashboardProps) {
  return (
    <KioskLayout title="Dashboard">
      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Companies" value={stats.companies} />
        <StatCard label="Plans" value={stats.plans} />
        <StatCard label="Users" value={stats.users} />
      </div>

      <div className="mt-6 overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-muted-foreground text-left">
            <tr>
              <th className="px-4 py-2 font-medium">Name</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Plan</th>
            </tr>
          </thead>
          <tbody>
            {companies.map((c) => (
              <tr key={c.id} className="border-t">
                <td className="px-4 py-2">{c.name}</td>
                <td className="px-4 py-2">
                  <span
                    className={
                      "rounded px-2 py-0.5 text-xs font-medium " +
                      (c.status === "active"
                        ? "bg-emerald-500/10 text-emerald-600"
                        : "bg-amber-500/10 text-amber-600")
                    }
                  >
                    {c.status}
                  </span>
                </td>
                <td className="px-4 py-2">{c.plan}</td>
              </tr>
            ))}
            {companies.length === 0 && (
              <tr>
                <td
                  colSpan={3}
                  className="text-muted-foreground px-4 py-6 text-center"
                >
                  No companies yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
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
