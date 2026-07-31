package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyInstalledKernelModulesUsesOwnershipAndEclassEvidence(t *testing.T) {
	inventory := InstalledInventory{Packages: []InstalledPackage{
		{
			ID:        PackageID{Category: "sys-fs", Name: "zfs-kmod", Version: "2.3"},
			Inherited: []string{"linux-info", "linux-mod-r1"},
			Contents: "obj /lib/modules/6.12.1/extra/zfs.ko.zst hash 1\n" +
				"obj /usr/lib/modules/6.12.1/extra/spl.ko hash 1\n",
		},
		{
			ID:        PackageID{Category: "app-misc", Name: "owned-module", Version: "1"},
			Contents:  "obj /lib/modules/6.13.0/extra/owned.ko.xz hash 1\n",
			Inherited: []string{"toolchain-funcs"},
		},
		{
			ID:        PackageID{Category: "app-misc", Name: "ordinary", Version: "1"},
			Contents:  "obj /usr/bin/ordinary hash 1\n",
			Inherited: []string{"linux-info"},
		},
	}}
	result := ClassifyInstalledKernelModules(inventory, "6.13.0")
	if len(result.Packages) != 2 {
		t.Fatalf("module inventory = %+v", result)
	}
	zfs := result.Packages[1]
	if zfs.Package.Name != "zfs-kmod" || zfs.Rebuild != KernelModuleTargetMissing || !zfs.NeedsRebuild ||
		len(zfs.Modules) != 2 || len(zfs.Evidence) != 3 {
		t.Fatalf("zfs module state = %+v", zfs)
	}
	owned := result.Packages[0]
	if owned.Rebuild != KernelModuleCurrent || owned.NeedsRebuild {
		t.Fatalf("owned module state = %+v", owned)
	}
}

func TestClassifyInstalledKernelModulesRepresentsUnknownArtifactState(t *testing.T) {
	inventory := InstalledInventory{Packages: []InstalledPackage{{
		ID:        PackageID{Category: "sys-kernel", Name: "module-source", Version: "1"},
		Inherited: []string{"linux-mod-r1"},
	}}}
	result := ClassifyInstalledKernelModules(inventory, "6.12")
	if len(result.Packages) != 1 || result.Packages[0].Rebuild != KernelModuleNoArtifacts ||
		result.Packages[0].NeedsRebuild {
		t.Fatalf("artifact state = %+v", result)
	}
	result = ClassifyInstalledKernelModules(InstalledInventory{Packages: []InstalledPackage{{
		ID:       PackageID{Category: "sys-kernel", Name: "module-source", Version: "1"},
		Contents: "obj /lib/modules/6.12/extra/module.ko hash 1\n",
	}}}, "")
	if result.Packages[0].Rebuild != KernelModuleRebuildNotEvaluated {
		t.Fatalf("untargeted state = %+v", result.Packages[0])
	}
}

func TestModuleFilesFromContentsIsRootAwareAndAdversarial(t *testing.T) {
	got := moduleFilesFromContents(`obj /lib/modules/6.12/extra/a.ko hash 1
obj /usr/lib/modules/6.13/extra/b.ko.gz hash 1
obj /lib/modules/6.12/kernel/drivers/in-tree.ko hash 1
obj /lib/modules/../../escape.ko hash 1
sym /lib/modules/6.12/extra/link.ko -> /outside 1
obj /usr/bin/not-a-module.ko hash 1
`)
	want := []KernelModuleFile{
		{Path: "/lib/modules/6.12/extra/a.ko", KernelRelease: "6.12"},
		{Path: "/usr/lib/modules/6.13/extra/b.ko.gz", KernelRelease: "6.13"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("module files = %+v, want %+v", got, want)
	}
}

func TestReadInstalledKernelModulesRejectsInvalidTargetRelease(t *testing.T) {
	_, err := ReadInstalledKernelModules(context.Background(), SystemPaths{}, InstalledKernelModuleOptions{
		TargetKernelRelease: "../../escape",
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("target release error = %v", err)
	}
}

func TestReadInstalledKernelModulesReadsContentsAndInheritedMetadata(t *testing.T) {
	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	record := filepath.Join(paths.VDB, "sys-fs", "zfs-kmod-2.3")
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"CONTENTS":   "obj /lib/modules/6.12/extra/zfs.ko hash 1\n",
		"EAPI":       "8\n",
		"SLOT":       "0\n",
		"repository": "gentoo\n",
		"INHERITED":  "linux-info linux-mod-r1\n",
	} {
		if err := os.WriteFile(filepath.Join(record, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ReadInstalledKernelModules(context.Background(), paths, InstalledKernelModuleOptions{
		Integrity: RequireComplete, TargetKernelRelease: "6.13",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) != 1 || !result.Packages[0].NeedsRebuild {
		t.Fatalf("read module inventory = %+v", result)
	}
}

func FuzzModuleKernelRelease(f *testing.F) {
	for _, seed := range []string{"/lib/modules/6.12/a.ko", "/usr/lib/modules/6.13/a.ko.xz", "/lib/modules/../../a.ko", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		release, ok := moduleKernelRelease(path)
		if ok && (release == "" || release == "." || release == ".." || strings.Contains(release, "/")) {
			t.Fatalf("unsafe release %q from %q", release, path)
		}
	})
}
