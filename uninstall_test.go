package kiosk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallLifecycle(t *testing.T) {
	root := t.TempDir()
	db := testDB(t)
	ctx := t.Context()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if _, err := ApplyInstall(root, plan, nil); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}

	// Nothing installed has data behind it yet, and nothing's been edited.
	uplan, err := PlanUninstall(ctx, root, db)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	if len(uplan.Files) != len(plan.Files) {
		t.Fatalf("expected %d tracked files, got %d", len(plan.Files), len(uplan.Files))
	}
	if got := countByStatus(uplan.Files, StatusUnchanged); got != len(uplan.Files) {
		t.Fatalf("expected every file StatusUnchanged, got %d/%d", got, len(uplan.Files))
	}
	for _, tc := range uplan.TableCounts {
		if tc.Count != 0 {
			t.Fatalf("expected %s to be empty, got %d", tc.Table, tc.Count)
		}
	}

	if err := ApplyUninstall(root, uplan, nil); err != nil {
		t.Fatalf("ApplyUninstall: %v", err)
	}
	for _, f := range uplan.Files {
		if _, err := os.Stat(filepath.Join(root, f.Path)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err=%v", f.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, lockFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err=%v", lockFileName, err)
	}
}

func TestUninstallReportsTableCounts(t *testing.T) {
	root := t.TempDir()
	db := testDB(t)
	ctx := t.Context()

	if _, err := ApplyInstall(root, mustPlan(t, root), nil); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}

	var plan planModel
	if err := db.Model(&planModel{}).Create(ctx, &plan, map[string]any{
		"uuid": "p1", "name": "Pro", "price_cents": 2900, "currency": "USD",
	}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	var company companyModel
	if err := db.Model(&companyModel{}).Create(ctx, &company, map[string]any{
		"uuid": "c1", "name": "Acme", "status": "active",
	}); err != nil {
		t.Fatalf("seed company: %v", err)
	}

	uplan, err := PlanUninstall(ctx, root, db)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	counts := map[string]int64{}
	for _, tc := range uplan.TableCounts {
		counts[tc.Table] = tc.Count
	}
	if counts["companies"] != 1 {
		t.Fatalf("expected companies=1, got %d", counts["companies"])
	}
	if counts["plans"] != 1 {
		t.Fatalf("expected plans=1, got %d", counts["plans"])
	}
	if counts["addons"] != 0 {
		t.Fatalf("expected addons=0, got %d", counts["addons"])
	}
}

func TestUninstallSkipsLocallyEditedFile(t *testing.T) {
	root := t.TempDir()
	db := testDB(t)
	ctx := t.Context()

	if _, err := ApplyInstall(root, mustPlan(t, root), nil); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}

	uplan, err := PlanUninstall(ctx, root, db)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	edited := uplan.Files[0]
	if err := os.WriteFile(filepath.Join(root, edited.Path), []byte("developer was here"), 0o644); err != nil {
		t.Fatal(err)
	}

	uplan2, err := PlanUninstall(ctx, root, db)
	if err != nil {
		t.Fatalf("PlanUninstall (after edit): %v", err)
	}
	for _, f := range uplan2.Files {
		want := StatusUnchanged
		if f.Path == edited.Path {
			want = StatusConflict
		}
		if f.Status != want {
			t.Fatalf("%s: expected %s, got %s", f.Path, want, f.Status)
		}
	}

	if err := ApplyUninstall(root, uplan2, map[string]bool{edited.Path: true}); err != nil {
		t.Fatalf("ApplyUninstall: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, edited.Path))
	if err != nil || string(got) != "developer was here" {
		t.Fatalf("expected the edited file to survive being skipped, got %q err=%v", got, err)
	}
	// Everything else Kiosk tracked is still gone.
	for _, f := range uplan2.Files {
		if f.Path == edited.Path {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f.Path)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err=%v", f.Path, err)
		}
	}
}

func mustPlan(t *testing.T, root string) *InstallPlan {
	t.Helper()
	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	return plan
}
