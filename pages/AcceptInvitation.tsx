import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useCsrfHeaders } from "@/lib/csrf";

interface AcceptInvitationProps {
  token: string;
  name: string;
  email: string;
  companyName: string;
  role: string;
}

// Self-contained, not KioskLayout — the person on this page has no account
// and isn't a developer, so none of Kiosk's own chrome (the "KIOSK" badge,
// the nav back to the dashboard) applies. Public route (see kiosk.go's
// Register: /kiosks/invitations/:token has no middleware at all), reached
// straight from an emailed link.
export default function AcceptInvitation({
  token,
  name,
  email,
  companyName,
  role,
}: AcceptInvitationProps) {
  const headers = { ...useCsrfHeaders(), Accept: "application/json" };
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError("Passwords don't match.");
      return;
    }
    setSubmitting(true);
    setError(null);
    const res = await fetch(`/kiosks/invitations/${token}`, {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setError(body.error ?? "Could not accept the invitation.");
      setSubmitting(false);
      return;
    }
    // Hard navigation — the accept call just authenticated this session.
    window.location.assign("/home");
  }

  return (
    <div className="bg-muted flex min-h-svh flex-col items-center justify-center gap-6 p-6 md:p-10">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <h1 className="text-xl font-medium">You&apos;re invited</h1>
          <p className="text-muted-foreground text-sm">
            Join <strong>{companyName}</strong> as <strong>{role}</strong>.
            Set a password to finish setting up {name}&apos;s account (
            {email}).
          </p>
        </div>

        <form onSubmit={submit} className="flex flex-col gap-4 rounded-lg border bg-background p-6">
          <div className="grid gap-2">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              autoFocus
              required
              minLength={8}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="password-confirm">Confirm password</Label>
            <Input
              id="password-confirm"
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              required
              minLength={8}
            />
          </div>
          {error && <p className="text-destructive text-sm">{error}</p>}
          <Button type="submit" disabled={submitting} className="w-full">
            {submitting ? "Setting up…" : "Accept invitation"}
          </Button>
        </form>
      </div>
    </div>
  );
}
