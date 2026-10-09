package kiosk

import (
	"context"
	"os"
	"path/filepath"

	"github.com/martin3zra/playsql"
)

// TableCount is the row count for one of Kiosk's own tables — reported so
// kiosk:uninstall can warn with real numbers instead of a generic notice.
type TableCount struct {
	Table string
	Count int64
}

// UninstallPlan is what kiosk:uninstall would do, computed without deleting
// anything.
type UninstallPlan struct {
	// Files are the ones .kiosk-lock currently tracks. StatusConflict means
	// its on-disk hash no longer matches what kiosk:install last wrote —
	// the developer edited it since.
	Files []PlannedFile
	// TableCounts lists Kiosk's own tables in a fixed order, each with its
	// current row count. Dropping the tables is left to the developer —
	// uninstall only ever removes the files it copied in.
	TableCounts []TableCount
}

// PlanUninstall reports what removing Kiosk from root would do.
func PlanUninstall(ctx context.Context, root string, db *playsql.DB) (*UninstallPlan, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, err
	}

	plan := &UninstallPlan{}
	for _, f := range lock.Files {
		onDiskHash, err := hashFile(filepath.Join(root, f.Path))
		if err != nil {
			continue // already gone — nothing to report or delete
		}
		status := StatusUnchanged
		if onDiskHash != f.SHA256 {
			status = StatusConflict
		}
		plan.Files = append(plan.Files, PlannedFile{Kind: f.Kind, Path: f.Path, Status: status, SHA256: f.SHA256})
	}

	// roles is deliberately not tracked here: unlike every other table,
	// it's never empty on a normal install (kiosk's own seed migration
	// always puts owner/admin in it), so counting it would flag every
	// uninstall as having "data at risk" even on a project that never
	// created a single company.
	counters := []struct {
		table string
		count func(context.Context) (int64, error)
	}{
		{"companies", func(ctx context.Context) (int64, error) { return db.Model(&companyModel{}).Count(ctx) }},
		{"plans", func(ctx context.Context) (int64, error) { return db.Model(&planModel{}).Count(ctx) }},
		{"addons", func(ctx context.Context) (int64, error) { return db.Model(&addonModel{}).Count(ctx) }},
		{"company_addons", func(ctx context.Context) (int64, error) { return db.Model(&companyAddonModel{}).Count(ctx) }},
		{"company_users", func(ctx context.Context) (int64, error) { return db.Model(&companyUserModel{}).Count(ctx) }},
		{"impersonation_sessions", func(ctx context.Context) (int64, error) { return db.Model(&impersonationSessionModel{}).Count(ctx) }},
		{"invitations", func(ctx context.Context) (int64, error) { return db.Model(&invitationModel{}).Count(ctx) }},
	}
	// Host mode never created companies, company_users or invitations —
	// the host's own tables stand in, and Kiosk mustn't count those.
	hostTables := map[string]bool{"companies": true, "company_users": true, "invitations": true}
	for _, c := range counters {
		if lock.Mode == Host.String() && hostTables[c.table] {
			continue
		}
		count, err := c.count(ctx)
		if err != nil {
			return nil, err
		}
		plan.TableCounts = append(plan.TableCounts, TableCount{Table: c.table, Count: count})
	}

	return plan, nil
}

// ApplyUninstall deletes every planned file except those named in skip
// (locally-edited files the developer chose to keep), then removes
// .kiosk-lock unconditionally — a kept file simply stops being Kiosk's to
// track. It never touches Kiosk's own database tables; see TableCounts.
func ApplyUninstall(root string, plan *UninstallPlan, skip map[string]bool) error {
	for _, pf := range plan.Files {
		if skip[pf.Path] {
			continue
		}
		if err := os.Remove(filepath.Join(root, pf.Path)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(filepath.Join(root, lockFileName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
