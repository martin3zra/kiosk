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
	Files []PlannedFile
}

// PlanInstall reports what installing/updating Kiosk into root would do.
func PlanInstall(root string) (*InstallPlan, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, err
	}

	plan := &InstallPlan{}
	for _, src := range sources {
		entries, err := fs.ReadDir(src.fsys, src.srcDir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
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
