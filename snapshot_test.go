package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func snapshotFixture(t *testing.T) SystemPaths {
	t.Helper()
	paths := effectiveConfigFixture(t)
	root := filepath.Dir(filepath.Dir(paths.ActiveProfile))
	paths.Root = root
	paths.VDB = filepath.Join(root, "var", "db", "pkg")
	paths.World = filepath.Join(root, "var", "lib", "portage", "world")
	writeInstalled(t, paths.VDB, "app-misc/example-1", nil)
	writeProfileFile(t, filepath.Dir(paths.World), "world", "app-misc/example\n")
	return paths
}

func TestReadSystemSnapshotReturnsConsistentCombinedState(t *testing.T) {
	paths := snapshotFixture(t)
	got, err := ReadSystemSnapshot(context.Background(), paths, SnapshotOptions{
		Installed: InstalledOptions{Integrity: RequireComplete, Workers: 2},
		Config:    ConfigOptions{Environment: []string{"USE=command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Installed.Packages) != 1 || got.Installed.Packages[0].ID.CPV() != "app-misc/example-1" {
		t.Fatalf("inventory = %+v", got.Installed)
	}
	if len(got.Selections.World) != 1 || got.Selections.World[0].Value != "app-misc/example" {
		t.Fatalf("world = %+v", got.Selections.World)
	}
	if got.Config.Profile == nil || len(got.Selections.System) == 0 || len(got.Config.CommandUse) != 1 {
		t.Fatalf("policy snapshot = %+v", got)
	}
}

func TestReadSystemSnapshotRejectsInvalidAttempts(t *testing.T) {
	_, err := ReadSystemSnapshot(context.Background(), SystemPaths{}, SnapshotOptions{Attempts: 1})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("error = %v", err)
	}
}

func TestReadSystemSnapshotDetectsPersistentMutation(t *testing.T) {
	paths := snapshotFixture(t)
	change := false
	_, err := ReadSystemSnapshot(context.Background(), paths, SnapshotOptions{
		Attempts:  3,
		Installed: InstalledOptions{Integrity: RequireComplete},
		betweenObservations: func(int) {
			change = !change
			value := "app-misc/example\n"
			if change {
				value = "app-editors/vim\n"
			}
			if writeErr := os.WriteFile(paths.World, []byte(value), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if !errors.Is(err, ErrConcurrentMutation) {
		t.Fatalf("error = %v", err)
	}
}

func TestReadSystemSnapshotHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadSystemSnapshot(ctx, SystemPaths{}, SnapshotOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestPortageLockPathMatchesEcosystemContract(t *testing.T) {
	if got := PortageStateLockPath("/var/db/pkg"); got != "/var/db/.pkg.portage_lockfile" {
		t.Fatalf("VDB lock path = %q", got)
	}
	if got := PortageStateLockPath("/var/lib/portage/world"); got != "/var/lib/portage/.world.portage_lockfile" {
		t.Fatalf("world lock path = %q", got)
	}
}
