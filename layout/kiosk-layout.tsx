import { Link, usePage } from "@inertiajs/react";
import { type PropsWithChildren } from "react";

import { useCsrfHeaders } from "@/lib/csrf";
import { type SharedProps } from "@/types";

// Kiosk's own shell — deliberately not AppShell/AppSidebar. Kiosk is a
// separate console bolted onto spark, not a section of the main app, so it
// gets its own distinct chrome (mirroring cactushr's DeveloperLayout.vue
// "DEV" badge) rather than inheriting the main nav.
export default function KioskLayout({
  title,
  children,
}: PropsWithChildren<{ title: string }>) {
  const { auth, kioskPendingApprovals } = usePage<SharedProps & {
    kioskPendingApprovals?: { count: number; url: string; label: string };
  }>().props;
  const headers = useCsrfHeaders();

  return (
    <div className="bg-background min-h-screen">
      <header className="border-b border-neutral-800 bg-neutral-950 text-neutral-100">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-6 py-3">
          <span className="rounded bg-amber-500 px-1.5 py-0.5 text-xs font-bold tracking-wide text-neutral-950">
            KIOSK
          </span>
          <span className="text-sm font-medium">{title}</span>
          <nav className="ml-6 flex items-center gap-4 text-sm text-neutral-400">
            <Link href="/kiosks" className="hover:text-neutral-100">
              Dashboard
            </Link>
          </nav>
          <div className="ml-auto flex items-center gap-4 text-xs text-neutral-400">
            {kioskPendingApprovals && kioskPendingApprovals.count > 0 && (
              <Link href={kioskPendingApprovals.url} className="rounded bg-amber-500 px-2 py-1 text-neutral-950">
                {kioskPendingApprovals.label} ({kioskPendingApprovals.count})
              </Link>
            )}
            {/* /home redirects developers straight back here unless they're
                impersonating (see spark's RedirectDeveloperToKiosk) — only
                show this link when it would actually go somewhere. */}
            {auth.isImpersonating && (
              <Link href="/home" className="hover:text-neutral-100">
                ← Back to app
              </Link>
            )}
            <Link
              method="post"
              href="/logout"
              as="button"
              headers={headers}
              className="hover:text-neutral-100"
            >
              Log out
            </Link>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-6 py-6">{children}</main>
    </div>
  );
}
