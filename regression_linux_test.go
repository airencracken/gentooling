//go:build linux

package gentooling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAuditUseReset(t *testing.T) {
	c := EffectiveConfig{ProfileUse: []FlagChange{{Name: "old", Enabled: true}}, UserUse: []FlagChange{{Name: "*", Enabled: false}, {Name: "new", Enabled: true}}}
	v, err := c.EvaluateUse(context.Background(), PackageContext{ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, DeclaredUse: []UseDeclaration{{Name: "old"}, {Name: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := v.Decision("old")
	if old.Enabled {
		t.Fatalf("USE=-* new retained inherited flag: %+v", old)
	}
}

func TestAuditChildUnforce(t *testing.T) {
	c := EffectiveConfig{Profile: &Profile{Layers: []ProfileLayer{{UseForce: []string{"flag"}}, {UseForce: []string{"-flag"}}}}}
	v, err := c.EvaluateUse(context.Background(), PackageContext{ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, DeclaredUse: []UseDeclaration{{Name: "flag"}}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := v.Decision("flag")
	if d.Enabled || d.Forced {
		t.Fatalf("child -flag did not remove parent force: %+v", d)
	}
}

func TestAuditPackageUnforce(t *testing.T) {
	c := EffectiveConfig{Profile: &Profile{Layers: []ProfileLayer{{PackageUseForce: []PackageFlagRule{{Atom: "cat/pkg", Flags: []string{"-flag"}}}}}}}
	v, err := c.EvaluateUse(context.Background(), PackageContext{ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, DeclaredUse: []UseDeclaration{{Name: "flag"}}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := v.Decision("flag")
	if d.Enabled || d.Forced {
		t.Fatalf("negative package force became positive force: %+v", d)
	}
}

func TestAuditLockChild(t *testing.T) {
	path := os.Getenv("GENTOO_AUDIT_LOCK")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.Exit(2)
	}
	l := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &l); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAuditOverlappingReadersKeepLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	one, err := observeStateLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	two, err := observeStateLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseStateLocks([]observedStateLock{two})
	writer := func() bool {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAuditLockChild$")
		cmd.Env = append(os.Environ(), "GENTOO_AUDIT_LOCK="+path)
		err := cmd.Run()
		if err == nil {
			return true
		}
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatalf("writer failed unexpectedly: %v", err)
		}
		return false
	}
	if writer() {
		t.Fatal("writer acquired while both readers held")
	}
	releaseStateLocks([]observedStateLock{one})
	if writer() {
		t.Fatal("writer acquired while second reader still held: first release dropped process-wide fcntl lock")
	}
	releaseStateLocks([]observedStateLock{two})
	if !writer() {
		t.Fatal("writer remained blocked after both readers released")
	}
}
