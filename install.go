package kiosk

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed migrations/*.yaml migrations/*.sql
var migrationsFS embed.FS

//go:embed pages/*.tsx
var pagesFS embed.FS

//go:embed layout/*.tsx
var layoutFS embed.FS

//go:embed mail/*.html
var mailFS embed.FS

// source maps one embedded directory (the module's canonical copy) to where
// kiosk:install writes it in a host project. camel and Vite both only look
// at fixed on-disk directories, so this is the manifest — there's no
// separate list to keep in sync: whatever's physically in these embedded
// directories is what ships.
type source struct {
	kind    string
	fsys    embed.FS
	srcDir  string
	destDir string
}

var sources = []source{
	{kind: "migration", fsys: migrationsFS, srcDir: "migrations", destDir: "database"},
	{kind: "page", fsys: pagesFS, srcDir: "pages", destDir: "resources/js/Pages/Kiosk"},
	{kind: "layout", fsys: layoutFS, srcDir: "layout", destDir: "resources/js/layouts"},
	{kind: "mail", fsys: mailFS, srcDir: "mail", destDir: "resources/views/mail"},
}

func sourceFor(kind string) source {
	for _, s := range sources {
		if s.kind == kind {
			return s
		}
	}
	panic("kiosk: unknown source kind " + kind)
}

// FileStatus is what PlanInstall/PlanUninstall found for one managed file.
type FileStatus string

const (
	// StatusNew: in Kiosk's embedded source, not yet on disk at all.
	StatusNew FileStatus = "new"
	// StatusUnchanged: on disk, matches what Kiosk would write — nothing to do.
	StatusUnchanged FileStatus = "unchanged"
	// StatusUpdated: on disk and untouched since kiosk:install last wrote it,
	// but the embedded source has since changed — safe to refresh.
	StatusUpdated FileStatus = "updated"
	// StatusConflict: on disk with a hash that doesn't match what Kiosk last
	// wrote (or was never tracked at all) — something Kiosk shouldn't
	// silently overwrite.
	StatusConflict FileStatus = "conflict"
)

// PlannedFile is one file PlanInstall or PlanUninstall reports on.
type PlannedFile struct {
	Kind   string
	Path   string // relative to the project root
	Status FileStatus
	SHA256 string // the hash Apply would write this file to
}

// InstallPlan is what kiosk:install would do, computed without touching disk.
type InstallPlan struct {
	Mode  TenancyMode
	Files []PlannedFile
}

// hostSkips are the files Host mode doesn't copy: Kiosk's own tenant,
// membership, role and invitation tables (and migrations that alter them),
// plus the invitation page and email. The host's own tables stand in.
var hostSkips = map[string]bool{
	"20260821120001_create_companies_table.yaml":              true,
	"20260821120004_create_company_users_table.yaml":          true,
	"20260824100000_create_roles_table.yaml":                  true,
	"20260824100001_seed_default_roles.sql":                   true,
	"20260824100002_add_identifier_to_companies_table.yaml":   true,
	"20260824100003_add_role_id_to_company_users_table.yaml":  true,
	"20260824150000_create_invitations_table.yaml":            true,
	"20260827150000_add_theme_preset_to_companies_table.yaml": true,
	"20260827150001_add_appearance_to_users_table.yaml":       true,
	"AcceptInvitation.tsx":                                    true,
	"kiosk-invitation.html":                                   true,
	"confirm-email.html":                                      true, // Owned mode's settings screens
}

// PlanInstall reports what installing/updating Kiosk into root would do,
// in the mode recorded in root's .kiosk-lock (Owned when there's none).
func PlanInstall(root string) (*InstallPlan, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, err
	}
	mode, err := ParseTenancyMode(lock.Mode)
	if err != nil {
		return nil, err
	}
	return PlanInstallMode(root, mode)
}

// PlanInstallMode is PlanInstall for an explicit mode (kiosk:install
// --mode host). Host mode leaves out hostSkips.
func PlanInstallMode(root string, mode TenancyMode) (*InstallPlan, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, err
	}

	plan := &InstallPlan{Mode: mode}
	for _, src := range sources {
		entries, err := fs.ReadDir(src.fsys, src.srcDir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || (mode == Host && hostSkips[entry.Name()]) {
				continue
			}
			data, err := fs.ReadFile(src.fsys, filepath.Join(src.srcDir, entry.Name()))
			if err != nil {
				return nil, err
			}
			embeddedHash := hashBytes(data)
			destPath := filepath.Join(src.destDir, entry.Name())

			plan.Files = append(plan.Files, PlannedFile{
				Kind:   src.kind,
				Path:   destPath,
				Status: planFileStatus(root, lock, destPath, embeddedHash),
				SHA256: embeddedHash,
			})
		}
	}
	return plan, nil
}

func planFileStatus(root string, lock *Lock, destPath, embeddedHash string) FileStatus {
	lockedEntry, inLock := lock.find(destPath)
	onDiskHash, err := hashFile(filepath.Join(root, destPath))
	onDisk := err == nil

	switch {
	case !inLock && !onDisk:
		return StatusNew
	case !inLock && onDisk && onDiskHash == embeddedHash:
		// Matches byte-for-byte but was never tracked — e.g. it was copied
		// in by hand before kiosk:install existed. Adopt it into the lock
		// rather than treating an exact match as a conflict.
		return StatusUnchanged
	case !inLock && onDisk:
		return StatusConflict
	case onDiskHash == lockedEntry.SHA256 && embeddedHash == lockedEntry.SHA256:
		return StatusUnchanged
	case onDiskHash == lockedEntry.SHA256 && embeddedHash != lockedEntry.SHA256:
		return StatusUpdated
	default:
		// on-disk hash doesn't match what Kiosk last wrote — the developer
		// edited it since.
		return StatusConflict
	}
}

// ApplyInstall writes every planned file except those named in skip
// (typically unresolved conflicts the developer chose to leave alone), and
// updates .kiosk-lock to match — including adopting already-matching files
// that weren't tracked yet, without rewriting them.
func ApplyInstall(root string, plan *InstallPlan, skip map[string]bool) (*Lock, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, err
	}
	lock.KioskVersion = moduleVersion()
	lock.Mode = ""
	if plan.Mode == Host {
		lock.Mode = plan.Mode.String()
	}

	for _, pf := range plan.Files {
		if skip[pf.Path] {
			continue
		}

		// StatusUnchanged (including a freshly adopted exact-match file)
		// needs no write, only the lock entry below. Anything else reaching
		// here — New, Updated, or a Conflict the caller didn't skip (i.e.
		// the developer confirmed the overwrite) — gets written.
		if pf.Status != StatusUnchanged {
			src := sourceFor(pf.Kind)
			data, err := fs.ReadFile(src.fsys, filepath.Join(src.srcDir, filepath.Base(pf.Path)))
			if err != nil {
				return nil, err
			}
			destAbs := filepath.Join(root, pf.Path)
			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(destAbs, data, 0o644); err != nil {
				return nil, err
			}
		}

		lock.upsert(LockedFile{Path: pf.Path, Kind: pf.Kind, SHA256: pf.SHA256})
	}

	if err := writeLock(root, lock); err != nil {
		return nil, err
	}
	return lock, nil
}
