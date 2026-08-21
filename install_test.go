package kiosk

import (
	"os"
	"path/filepath"
	"testing"
)

func countByStatus(files []PlannedFile, status FileStatus) int {
	n := 0
	for _, f := range files {
		if f.Status == status {
			n++
		}
	}
	return n
}

func TestPlanInstallFresh(t *testing.T) {
	root := t.TempDir()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if len(plan.Files) == 0 {
		t.Fatal("expected planned files, got none")
	}
	if got := countByStatus(plan.Files, StatusNew); got != len(plan.Files) {
		t.Fatalf("expected every file StatusNew on a fresh root, got %d/%d", got, len(plan.Files))
	}

	lock, err := ApplyInstall(root, plan, nil)
	if err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}
	if lock.KioskVersion == "" {
		t.Fatal("expected a non-empty KioskVersion")
	}
	if len(lock.Files) != len(plan.Files) {
		t.Fatalf("expected %d lock entries, got %d", len(plan.Files), len(lock.Files))
	}
	for _, f := range plan.Files {
		if _, err := os.Stat(filepath.Join(root, f.Path)); err != nil {
			t.Fatalf("expected %s to exist after install: %v", f.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, lockFileName)); err != nil {
		t.Fatalf("expected %s to exist after install: %v", lockFileName, err)
	}
}

func TestPlanInstallReRunUnchanged(t *testing.T) {
	root := t.TempDir()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if _, err := ApplyInstall(root, plan, nil); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}

	plan2, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall (2nd run): %v", err)
	}
	if got := countByStatus(plan2.Files, StatusUnchanged); got != len(plan2.Files) {
		t.Fatalf("expected every file StatusUnchanged on re-run, got %d/%d", got, len(plan2.Files))
	}
}

func TestPlanInstallAdoptsExactMatchWithoutLock(t *testing.T) {
	root := t.TempDir()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}

	// Simulate files that were copied in by hand before kiosk:install
	// existed — write the exact bytes the plan says it would install,
	// without ever going through ApplyInstall (so no lock exists yet).
	for _, f := range plan.Files {
		src := sourceFor(f.Kind)
		data, err := os.ReadFile(filepath.Join(rootOf(src), filepath.Base(f.Path)))
		if err != nil {
			t.Fatalf("read embedded fixture: %v", err)
		}
		dest := filepath.Join(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plan2, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall (adopt check): %v", err)
	}
	if got := countByStatus(plan2.Files, StatusUnchanged); got != len(plan2.Files) {
		t.Fatalf("expected byte-for-byte matches to be adopted as StatusUnchanged, got %d/%d", got, len(plan2.Files))
	}

	lock, err := ApplyInstall(root, plan2, nil)
	if err != nil {
		t.Fatalf("ApplyInstall (adopt): %v", err)
	}
	if len(lock.Files) != len(plan2.Files) {
		t.Fatalf("expected adopted files to land in the lock, got %d/%d", len(lock.Files), len(plan2.Files))
	}
}

func TestPlanInstallConflictsWithUntrackedFile(t *testing.T) {
	root := t.TempDir()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	target := plan.Files[0]
	dest := filepath.Join(root, target.Path)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("not what kiosk would write"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan2, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall (conflict check): %v", err)
	}
	var status FileStatus
	for _, f := range plan2.Files {
		if f.Path == target.Path {
			status = f.Status
		}
	}
	if status != StatusConflict {
		t.Fatalf("expected %s to be StatusConflict, got %s", target.Path, status)
	}

	// Skipped: the untracked file survives untouched.
	if _, err := ApplyInstall(root, plan2, map[string]bool{target.Path: true}); err != nil {
		t.Fatalf("ApplyInstall (skip conflict): %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "not what kiosk would write" {
		t.Fatalf("expected skipped conflict file to be left alone, got %q", got)
	}

	// Not skipped: confirmed overwrite replaces it.
	if _, err := ApplyInstall(root, plan2, nil); err != nil {
		t.Fatalf("ApplyInstall (confirm overwrite): %v", err)
	}
	got, _ = os.ReadFile(dest)
	if string(got) == "not what kiosk would write" {
		t.Fatal("expected confirmed overwrite to replace the file")
	}
}

func TestPlanInstallDetectsLocalEdit(t *testing.T) {
	root := t.TempDir()

	plan, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if _, err := ApplyInstall(root, plan, nil); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}

	edited := plan.Files[0]
	dest := filepath.Join(root, edited.Path)
	if err := os.WriteFile(dest, []byte("developer was here"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan2, err := PlanInstall(root)
	if err != nil {
		t.Fatalf("PlanInstall (edit check): %v", err)
	}
	for _, f := range plan2.Files {
		want := StatusUnchanged
		if f.Path == edited.Path {
			want = StatusConflict
		}
		if f.Status != want {
			t.Fatalf("%s: expected %s, got %s", f.Path, want, f.Status)
		}
	}
}

func TestPlanFileStatusUpdated(t *testing.T) {
	root := t.TempDir()
	lock := &Lock{Files: []LockedFile{{Path: "database/x.yaml", Kind: "migration", SHA256: "old-hash"}}}

	dest := filepath.Join(root, "database/x.yaml")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	// On disk matches the lock (untouched by the developer)...
	if err := os.WriteFile(dest, []byte("old-hash"), 0o644); err != nil {
		t.Fatal(err)
	}
	onDiskHash, _ := hashFile(dest)
	lock.Files[0].SHA256 = onDiskHash

	// ...but the embedded source has since moved on to a new hash.
	got := planFileStatus(root, lock, "database/x.yaml", "a-newer-hash")
	if got != StatusUpdated {
		t.Fatalf("expected StatusUpdated, got %s", got)
	}
}

// rootOf resolves an embedded source's fixture directory back to a real
// filesystem path, for tests that need to read the same bytes ApplyInstall
// would without going through ApplyInstall itself.
func rootOf(src source) string {
	return filepath.Join(".", src.srcDir)
}
