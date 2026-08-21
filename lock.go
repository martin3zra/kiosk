package kiosk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
)

const lockFileName = ".kiosk-lock"

// LockedFile is one entry in .kiosk-lock: a file kiosk:install copied into
// the host project, and the hash it wrote — the baseline later runs diff
// against to tell "untouched, safe to refresh" from "the developer edited
// this" apart.
type LockedFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// Lock is the on-disk shape of .kiosk-lock — plain JSON, per the technical
// spec: which Kiosk version wrote each file, and that file's hash.
type Lock struct {
	KioskVersion string       `json:"kiosk_version"`
	Files        []LockedFile `json:"files"`
}

func readLock(root string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(root, lockFileName))
	if os.IsNotExist(err) {
		return &Lock{}, nil
	}
	if err != nil {
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	return &lock, nil
}

func writeLock(root string, lock *Lock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, lockFileName), data, 0o644)
}

func (l *Lock) find(path string) (LockedFile, bool) {
	for _, f := range l.Files {
		if f.Path == path {
			return f, true
		}
	}
	return LockedFile{}, false
}

func (l *Lock) upsert(f LockedFile) {
	for i, existing := range l.Files {
		if existing.Path == f.Path {
			l.Files[i] = f
			return
		}
	}
	l.Files = append(l.Files, f)
}

// moduleVersion reads the running binary's own build info to find which
// version of github.com/martin3zra/kiosk it was built against — nothing
// kiosk can know about itself from the inside.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/martin3zra/kiosk" {
			return dep.Version
		}
	}
	return "dev"
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}
