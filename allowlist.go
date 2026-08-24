package kiosk

import "strings"

// AllowlistConfig is the developer-allowlist shape a host app's own Config
// type can embed directly (e.g. Config.kiosk kiosk.AllowlistConfig), so the
// type backing "who's a kiosk developer" lives here rather than being
// redefined by every host app.
type AllowlistConfig struct {
	Developers map[string]bool
}

// ParseAllowlist turns a comma-separated KIOSK_DEVELOPERS-style value into
// the lowercased-email set Options.Allowlist (and AllowlistConfig.Developers)
// expects.
func ParseAllowlist(raw string) map[string]bool {
	allowlist := map[string]bool{}
	for _, email := range strings.Split(raw, ",") {
		email = strings.ToLower(strings.TrimSpace(email))
		if email != "" {
			allowlist[email] = true
		}
	}
	return allowlist
}
