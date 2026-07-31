package gentooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadRepositoriesRebasesLocationsAndOrdersMasters(t *testing.T) {
	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	master := filepath.Join(root, "var", "db", "repos", "gentoo")
	child := filepath.Join(root, "var", "db", "repos", "overlay")
	writeProfileFile(t, filepath.Join(child, "metadata"), "layout.conf", "masters = gentoo\n")
	writeProfileFile(t, filepath.Join(master, "metadata"), "layout.conf", "")
	writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf", `[DEFAULT]
main-repo = gentoo

[overlay]
location = /var/db/repos/overlay
sync-type = git
sync-uri = https://example.test/overlay.git
priority = 50
auto-sync = no

[gentoo]
location = /var/db/repos/gentoo
sync-type = git
sync-uri = https://example.test/gentoo.git
clone-depth = 5
sync-depth = 0
`)

	got, err := ReadRepositories(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "gentoo" || got[1].Name != "overlay" {
		t.Fatalf("repository order = %+v", got)
	}
	if got[0].Location != master || !got[0].Main || got[0].CloneDepth == nil || *got[0].CloneDepth != 5 ||
		got[0].SyncDepth == nil || *got[0].SyncDepth != 0 {
		t.Fatalf("root-aware main repository = %+v", got[0])
	}
	if got[1].Location != child || got[1].Priority != 50 || got[1].AutoSync ||
		!reflect.DeepEqual(got[1].Masters, []string{"gentoo"}) {
		t.Fatalf("overlay = %+v", got[1])
	}
	if got[0].Source.Path != paths.ReposConf || got[0].Source.Line == 0 {
		t.Fatalf("repository provenance = %+v", got[0].Source)
	}
}

func TestReadRepositoriesAcceptsAlreadyRootedFixtureLocation(t *testing.T) {
	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	location := filepath.Join(root, "repositories", "gentoo")
	writeProfileFile(t, filepath.Join(location, "metadata"), "layout.conf", "")
	writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf",
		fmt.Sprintf("[gentoo]\nlocation = %s\n", location))
	got, err := ReadRepositories(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Location != location {
		t.Fatalf("repository = %+v", got)
	}
}

func TestReadRepositoriesRejectsCycleInvalidDepthAndSymlink(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		root := t.TempDir()
		paths := DefaultSystemPaths(root)
		for name, masters := range map[string]string{"a": "b", "b": "a"} {
			writeProfileFile(t, filepath.Join(root, "repos", name, "metadata"), "layout.conf", "masters = "+masters+"\n")
		}
		writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf",
			fmt.Sprintf("[a]\nlocation = %s\n[b]\nlocation = %s\n", filepath.Join(root, "repos", "a"), filepath.Join(root, "repos", "b")))
		_, err := ReadRepositories(context.Background(), paths)
		if !errors.Is(err, ErrRepositoryCycle) {
			t.Fatalf("cycle error = %v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		paths := DefaultSystemPaths(t.TempDir())
		writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf", "[gentoo]\nlocation = /repo\nclone-depth = -1\n")
		_, err := ReadRepositories(context.Background(), paths)
		if !errors.Is(err, ErrInvalidData) {
			t.Fatalf("depth error = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		paths := DefaultSystemPaths(t.TempDir())
		if err := os.MkdirAll(paths.ReposConf, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "repo.conf")
		if err := os.WriteFile(outside, []byte("[gentoo]\nlocation = /repo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(paths.ReposConf, "escape.conf")); err != nil {
			t.Fatal(err)
		}
		_, err := ReadRepositories(context.Background(), paths)
		if !errors.Is(err, ErrInvalidData) {
			t.Fatalf("symlink error = %v", err)
		}
	})
}

func TestReadRepositoriesHonorsCancellationAndOwnsResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadRepositories(ctx, SystemPaths{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	location := filepath.Join(root, "repo")
	writeProfileFile(t, filepath.Join(location, "metadata"), "layout.conf", "")
	writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf", fmt.Sprintf("[repo]\nlocation = %s\n", location))
	first, err := ReadRepositories(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Name = "mutated"
	second, err := ReadRepositories(context.Background(), paths)
	if err != nil || second[0].Name != "repo" {
		t.Fatalf("owned repositories = %+v, %v", second, err)
	}
}

func TestReadEffectiveConfigDiscoversRepositoriesForCrossRepositoryProfile(t *testing.T) {
	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	gentoo := filepath.Join(root, "var", "db", "repos", "gentoo")
	overlay := filepath.Join(root, "var", "db", "repos", "overlay")
	base := filepath.Join(gentoo, "profiles", "base")
	activeProfile := filepath.Join(overlay, "profiles", "default")
	writeProfileFile(t, base, "make.defaults", "ARCH=amd64\n")
	writeProfileFile(t, activeProfile, "parent", "gentoo:base\n")
	writeProfileFile(t, activeProfile, "make.defaults", "USE=overlay\n")
	writeProfileFile(t, filepath.Join(gentoo, "metadata"), "layout.conf", "")
	writeProfileFile(t, filepath.Join(overlay, "metadata"), "layout.conf", "masters = gentoo\n")
	writeProfileFile(t, filepath.Dir(paths.ReposConf), "repos.conf",
		"[overlay]\nlocation = /var/db/repos/overlay\n[gentoo]\nlocation = /var/db/repos/gentoo\n")
	writeProfileFile(t, filepath.Dir(paths.MakeGlobals), "make.globals", "USE=\n")
	if err := os.MkdirAll(filepath.Dir(paths.ActiveProfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(activeProfile, paths.ActiveProfile); err != nil {
		t.Fatal(err)
	}

	config, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Repositories) != 2 || config.Repositories[0].Name != "gentoo" ||
		config.Profile == nil || !reflect.DeepEqual(config.Profile.Directories, []string{base, activeProfile}) {
		t.Fatalf("discovered configuration = %+v", config)
	}
}
