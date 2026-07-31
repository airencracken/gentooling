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
	if got.Consistency != LockedAndStabilized {
		t.Fatalf("consistency = %v", got.Consistency)
	}
}

func TestReadSystemSnapshotSupportsExplicitStabilizedLocklessMode(t *testing.T) {
	paths := snapshotFixture(t)
	got, err := ReadSystemSnapshot(context.Background(), paths, SnapshotOptions{
		Consistency: StabilizedLockless,
		Installed:   InstalledOptions{Integrity: RequireComplete},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Consistency != StabilizedLockless {
		t.Fatalf("consistency = %v", got.Consistency)
	}
}

func TestReadSystemSnapshotBindsProspectiveVisibilityAndUse(t *testing.T) {
	paths := snapshotFixture(t)
	repository := paths.Repositories[0].Path
	writeCandidate(t, repository, "app-misc", "example-2", "EAPI=8\nSLOT=0/2\nKEYWORDS=amd64\nIUSE=+ssl feature\n")
	got, err := ReadSystemSnapshot(context.Background(), paths, SnapshotOptions{
		Installed:         InstalledOptions{Integrity: RequireComplete},
		IncludeCandidates: true,
		Candidates:        CandidateOptions{Integrity: RequireComplete},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := got.EvaluateCandidate(context.Background(), PackageID{
		Category: "app-misc", Name: "example", Version: "2", Repository: "gentoo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.Visibility.Visible || !evaluation.Visibility.Stable {
		t.Fatalf("visibility = %+v", evaluation.Visibility)
	}
	ssl, found := evaluation.Use.Decision("ssl")
	if !found || !ssl.Enabled {
		t.Fatalf("USE evaluation = %+v", evaluation.Use)
	}
	evaluation.Candidate.Keywords[0] = "mutated"
	again, err := got.EvaluateCandidate(context.Background(), PackageID{
		Category: "app-misc", Name: "example", Version: "2", Repository: "gentoo",
	})
	if err != nil || again.Candidate.Keywords[0] != "amd64" {
		t.Fatalf("snapshot candidate ownership = %+v, %v", again, err)
	}
}

func TestEvaluateCandidateRejectsMissingAndAmbiguousEvidence(t *testing.T) {
	snapshot := SystemSnapshot{Candidates: CandidateInventory{Candidates: []RepositoryCandidate{
		{ID: PackageID{Category: "app-misc", Name: "example", Version: "1", Repository: "a"}},
		{ID: PackageID{Category: "app-misc", Name: "example", Version: "1", Repository: "b"}},
	}}}
	_, err := snapshot.EvaluateCandidate(context.Background(), PackageID{Category: "app-misc", Name: "missing", Version: "1"})
	if !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("missing error = %v", err)
	}
	_, err = snapshot.EvaluateCandidate(context.Background(), PackageID{Category: "app-misc", Name: "example", Version: "1"})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("ambiguous error = %v", err)
	}
}

func TestReadSystemSnapshotDetectsCandidateMutationBetweenObservations(t *testing.T) {
	paths := snapshotFixture(t)
	repository := paths.Repositories[0].Path
	cache := writeCandidate(t, repository, "app-misc", "example-2", "EAPI=8\nSLOT=0\nKEYWORDS=amd64\nIUSE=ssl\n")
	change := false
	_, err := ReadSystemSnapshot(context.Background(), paths, SnapshotOptions{
		Attempts:          3,
		Consistency:       StabilizedLockless,
		Installed:         InstalledOptions{Integrity: RequireComplete},
		IncludeCandidates: true,
		Candidates:        CandidateOptions{Integrity: RequireComplete},
		betweenObservations: func(int) {
			change = !change
			keywords := "amd64"
			if change {
				keywords = "~amd64"
			}
			if writeErr := os.WriteFile(cache, []byte("EAPI=8\nSLOT=0\nKEYWORDS="+keywords+"\nIUSE=ssl\n"), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if !errors.Is(err, ErrConcurrentMutation) {
		t.Fatalf("candidate mutation error = %v", err)
	}
}

func TestReadSystemSnapshotRejectsUnknownConsistencyMode(t *testing.T) {
	_, err := ReadSystemSnapshot(context.Background(), SystemPaths{}, SnapshotOptions{
		Consistency: SnapshotConsistency(255),
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("error = %v", err)
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
