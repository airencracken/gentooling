package gentooling

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type KernelModuleEvidenceKind string

const (
	KernelModuleOwnedFile      KernelModuleEvidenceKind = "owned_module_file"
	KernelModuleInheritedClass KernelModuleEvidenceKind = "inherited_module_eclass"
)

type KernelModuleEvidence struct {
	Kind  KernelModuleEvidenceKind
	Value string
}

type KernelModuleFile struct {
	Path          string
	KernelRelease string
}

type KernelModuleRebuildState string

const (
	KernelModuleRebuildNotEvaluated KernelModuleRebuildState = "not_evaluated"
	KernelModuleCurrent             KernelModuleRebuildState = "current"
	KernelModuleTargetMissing       KernelModuleRebuildState = "target_missing"
	KernelModuleNoArtifacts         KernelModuleRebuildState = "no_module_artifacts"
)

type InstalledKernelModulePackage struct {
	Package      PackageID
	Modules      []KernelModuleFile
	Evidence     []KernelModuleEvidence
	TargetKernel string
	Rebuild      KernelModuleRebuildState
	NeedsRebuild bool
}

type InstalledKernelModuleInventory struct {
	Packages []InstalledKernelModulePackage
	Issues   []Issue
}

type InstalledKernelModuleOptions struct {
	Integrity           IntegrityMode
	Workers             int
	TargetKernelRelease string
}

var kernelReleasePattern = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// ReadInstalledKernelModules classifies installed out-of-tree kernel-module
// packages from VDB eclass and file-ownership evidence. TargetKernelRelease is
// explicit; Gentooling never consults uname.
func ReadInstalledKernelModules(ctx context.Context, paths SystemPaths, options InstalledKernelModuleOptions) (InstalledKernelModuleInventory, error) {
	if options.TargetKernelRelease != "" &&
		(!kernelReleasePattern.MatchString(options.TargetKernelRelease) ||
			options.TargetKernelRelease == "." || options.TargetKernelRelease == "..") {
		return InstalledKernelModuleInventory{}, fmt.Errorf("%w: invalid target kernel release %q", ErrInvalidData, options.TargetKernelRelease)
	}
	inventory, err := ReadInstalled(ctx, paths, InstalledOptions{
		Integrity: options.Integrity, IncludeContents: true, Workers: options.Workers,
	})
	result := ClassifyInstalledKernelModules(inventory, options.TargetKernelRelease)
	if err != nil {
		return result, err
	}
	return result, nil
}

// ClassifyInstalledKernelModules derives module and rebuild state from a
// caller-owned installed inventory.
func ClassifyInstalledKernelModules(inventory InstalledInventory, targetKernelRelease string) InstalledKernelModuleInventory {
	result := InstalledKernelModuleInventory{Issues: append([]Issue(nil), inventory.Issues...)}
	for _, installed := range inventory.Packages {
		var evidence []KernelModuleEvidence
		for _, inherited := range installed.Inherited {
			if strings.HasPrefix(inherited, "linux-mod") {
				evidence = append(evidence, KernelModuleEvidence{Kind: KernelModuleInheritedClass, Value: inherited})
			}
		}
		modules := moduleFilesFromContents(installed.Contents)
		for _, module := range modules {
			evidence = append(evidence, KernelModuleEvidence{Kind: KernelModuleOwnedFile, Value: module.Path})
		}
		if len(evidence) == 0 {
			continue
		}
		item := InstalledKernelModulePackage{
			Package: installed.ID, Modules: modules, Evidence: evidence,
			TargetKernel: targetKernelRelease, Rebuild: KernelModuleRebuildNotEvaluated,
		}
		switch {
		case len(modules) == 0:
			item.Rebuild = KernelModuleNoArtifacts
		case targetKernelRelease == "":
			item.Rebuild = KernelModuleRebuildNotEvaluated
		case hasModuleForKernel(modules, targetKernelRelease):
			item.Rebuild = KernelModuleCurrent
		default:
			item.Rebuild, item.NeedsRebuild = KernelModuleTargetMissing, true
		}
		result.Packages = append(result.Packages, item)
	}
	sort.Slice(result.Packages, func(i, j int) bool { return result.Packages[i].Package.CPV() < result.Packages[j].Package.CPV() })
	return result
}

func moduleFilesFromContents(contents string) []KernelModuleFile {
	var result []KernelModuleFile
	for _, raw := range strings.Split(contents, "\n") {
		fields := strings.Fields(raw)
		if len(fields) < 2 || fields[0] != "obj" || !kernelModulePath(fields[1]) {
			continue
		}
		release, ok := moduleKernelRelease(fields[1])
		if !ok || inTreeKernelModule(fields[1], release) {
			continue
		}
		result = append(result, KernelModuleFile{Path: fields[1], KernelRelease: release})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func inTreeKernelModule(path, release string) bool {
	marker := "/modules/" + release + "/kernel/"
	return strings.Contains(path, marker)
}

func kernelModulePath(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range []string{".ko", ".ko.gz", ".ko.xz", ".ko.zst"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func moduleKernelRelease(path string) (string, bool) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", false
	}
	clean := strings.TrimPrefix(path, "/")
	parts := strings.Split(clean, "/")
	if len(parts) < 4 || parts[0] != "lib" && !(len(parts) >= 5 && parts[0] == "usr" && parts[1] == "lib") {
		return "", false
	}
	index := 1
	if parts[0] == "usr" {
		index = 2
	}
	if parts[index] != "modules" || parts[index+1] == "" || parts[index+1] == "." || parts[index+1] == ".." {
		return "", false
	}
	return parts[index+1], true
}

func hasModuleForKernel(modules []KernelModuleFile, release string) bool {
	for _, module := range modules {
		if module.KernelRelease == release {
			return true
		}
	}
	return false
}

func (state KernelModuleRebuildState) Validate() error {
	switch state {
	case KernelModuleRebuildNotEvaluated, KernelModuleCurrent, KernelModuleTargetMissing, KernelModuleNoArtifacts:
		return nil
	default:
		return fmt.Errorf("%w: unknown kernel module rebuild state %q", ErrInvalidData, state)
	}
}
