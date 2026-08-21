import { Link } from "@inertiajs/react";
import { type PropsWithChildren } from "react";

// Kiosk's own shell — deliberately not AppShell/AppSidebar. Kiosk is a
// separate console bolted onto spark, not a section of the main app, so it
// gets its own distinct chrome (mirroring cactushr's DeveloperLayout.vue
// "DEV" badge) rather than inheriting the main nav.
export default function KioskLayout({
  title,
  children,
}: PropsWithChildren<{ title: string }>) {
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
          <div className="ml-auto">
            <Link
              href="/home"
              className="text-xs text-neutral-400 hover:text-neutral-100"
            >
              ← Back to app
            </Link>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-6 py-6">{children}</main>
    </div>
  );
}
