package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeProfileFile(t *testing.T, directory, name, value string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func profileFixture(t *testing.T) (SystemPaths, string, string) {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "repos", "gentoo")
	base := filepath.Join(repository, "profiles", "base")
	leaf := filepath.Join(repository, "profiles", "default", "linux", "amd64")
	writeProfileFile(t, base, "make.defaults", "ARCH=\"amd64\"\nUSE=\"base\"\n")
	writeProfileFile(t, base, "packages", "*sys-apps/baselayout\n*sys-libs/glibc\n")
	writeProfileFile(t, base, "use.force", "base-force\n")
	writeProfileFile(t, base, "package.use", "# ignored\napp-misc/example base\n")
	writeProfileFile(t, leaf, "parent", "../../../base\n")
	writeProfileFile(t, leaf, "make.defaults", "USE=\"leaf\"\nCHOST=\"x86_64-pc-linux-gnu\"\n")
	writeProfileFile(t, leaf, "packages", "-*sys-apps/baselayout\n*sys-apps/openrc\n")
	writeProfileFile(t, leaf, "use.force", "-base-force\nleaf-force\n")
	writeProfileFile(t, leaf, "package.use", "app-misc/example leaf -base\n")
	active := filepath.Join(root, "etc", "portage", "make.profile")
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(leaf, active); err != nil {
		t.Fatal(err)
	}
	return SystemPaths{
		ActiveProfile: active,
		Repositories:  []RepositoryPath{{Name: "gentoo", Path: repository}},
	}, base, leaf
}

func TestReadProfileReturnsOrderedGraphPolicyAndProvenance(t *testing.T) {
	paths, base, leaf := profileFixture(t)
	got, err := ReadProfile(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActivePath != leaf || !reflect.DeepEqual(got.Directories, []string{base, leaf}) {
		t.Fatalf("profile identity/order = %+v", got)
	}
	if got.MakeDefaults["ARCH"] != "amd64" || got.MakeDefaults["USE"] != "leaf" ||
		got.MakeDefaults["CHOST"] != "x86_64-pc-linux-gnu" {
		t.Fatalf("make.defaults merge = %+v", got.MakeDefaults)
	}
	if !reflect.DeepEqual(got.System, []string{"sys-libs/glibc", "sys-apps/openrc"}) {
		t.Fatalf("@system merge = %v", got.System)
	}
	if !reflect.DeepEqual(got.UseForce, []string{"leaf-force"}) {
		t.Fatalf("use.force merge = %v", got.UseForce)
	}
	if len(got.PackageUse) != 2 || got.PackageUse[0].Source.Path != filepath.Join(base, "package.use") ||
		got.PackageUse[0].Source.Line != 2 || got.PackageUse[1].Source.Path != filepath.Join(leaf, "package.use") {
		t.Fatalf("ordered package.use provenance = %+v", got.PackageUse)
	}
	if len(got.Layers) != 2 || got.Layers[0].Path != base || got.Layers[1].Path != leaf {
		t.Fatalf("profile layers = %+v", got.Layers)
	}
}

func TestReadProfileOwnsReturnedCollections(t *testing.T) {
	paths, _, _ := profileFixture(t)
	first, err := ReadProfile(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	first.MakeDefaults["ARCH"] = "mutated"
	first.Layers[0].PackageUse[0].Flags[0] = "mutated"
	second, err := ReadProfile(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if second.MakeDefaults["ARCH"] != "amd64" || second.Layers[0].PackageUse[0].Flags[0] != "base" {
		t.Fatalf("caller mutation leaked into subsequent result: %+v", second)
	}
}

func TestReadProfileResolvesExplicitCrossRepositoryParent(t *testing.T) {
	root := t.TempDir()
	gentoo := filepath.Join(root, "repos", "gentoo")
	custom := filepath.Join(root, "repos", "custom")
	base := filepath.Join(gentoo, "profiles", "base")
	leaf := filepath.Join(custom, "profiles", "default")
	writeProfileFile(t, base, "use.mask", "masked\n")
	writeProfileFile(t, leaf, "parent", "gentoo:base\n")
	active := filepath.Join(root, "make.profile")
	if err := os.Symlink(leaf, active); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProfile(context.Background(), SystemPaths{
		ActiveProfile: active,
		Repositories: []RepositoryPath{
			{Name: "gentoo", Path: gentoo},
			{Name: "custom", Path: custom},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Directories, []string{base, leaf}) || !reflect.DeepEqual(got.UseMask, []string{"masked"}) {
		t.Fatalf("cross-repository profile = %+v", got)
	}
}

func TestReadProfileRejectsEscapesCyclesAndPolicySymlinks(t *testing.T) {
	t.Run("escape", func(t *testing.T) {
		paths, _, leaf := profileFixture(t)
		writeProfileFile(t, leaf, "parent", "../../../../../../outside\n")
		_, err := ReadProfile(context.Background(), paths)
		if !errors.Is(err, ErrInvalidData) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		paths, base, _ := profileFixture(t)
		writeProfileFile(t, base, "parent", "../default/linux/amd64\n")
		_, err := ReadProfile(context.Background(), paths)
		if !errors.Is(err, ErrProfileCycle) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("policy symlink", func(t *testing.T) {
		paths, _, leaf := profileFixture(t)
		outside := filepath.Join(t.TempDir(), "use.mask")
		if err := os.WriteFile(outside, []byte("escaped\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(leaf, "use.mask")); err != nil {
			t.Fatal(err)
		}
		_, err := ReadProfile(context.Background(), paths)
		if !errors.Is(err, ErrInvalidData) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestReadProfileRejectsMalformedRulesAndHonorsCancellation(t *testing.T) {
	paths, _, leaf := profileFixture(t)
	writeProfileFile(t, leaf, "package.use.mask", "missing-flags\n")
	_, err := ReadProfile(context.Background(), paths)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("malformed rule error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadProfile(ctx, paths)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestReadProfileRejectsUnknownCrossRepositoryParent(t *testing.T) {
	paths, _, leaf := profileFixture(t)
	writeProfileFile(t, leaf, "parent", "unknown:base\n")
	_, err := ReadProfile(context.Background(), paths)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("error = %v", err)
	}
}
