package gentooling_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestInstalledInventoryPublicContract(t *testing.T) {
	paths := gentooling.DefaultSystemPaths(t.TempDir())
	_, err := gentooling.ReadInstalled(context.Background(), paths, gentooling.InstalledOptions{
		Integrity: gentooling.RequireComplete,
		Workers:   2,
	})
	if err == nil {
		t.Fatal("missing fixture VDB unexpectedly succeeded")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapped error contract is not discoverable: %v", err)
	}

	declaration := gentooling.UseDeclaration{Name: "ssl", Default: gentooling.UseDefaultEnabled}
	if declaration.Name != "ssl" {
		t.Fatalf("public declaration contract changed: %+v", declaration)
	}
	installed := gentooling.InstalledPackage{RequiredUse: "ssl? ( !test )"}
	if installed.RequiredUse == "" {
		t.Fatal("installed REQUIRED_USE is absent from the public contract")
	}
}

func TestProfilePublicContract(t *testing.T) {
	_, err := gentooling.ReadProfile(context.Background(), gentooling.SystemPaths{
		ActiveProfile: "/definitely/missing/make.profile",
		Repositories: []gentooling.RepositoryPath{
			{Name: "gentoo", Path: "/var/db/repos/gentoo"},
		},
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile filesystem error is not discoverable: %v", err)
	}

	rule := gentooling.PackageFlagRule{
		Atom:   "app-misc/example",
		Flags:  []string{"feature"},
		Source: gentooling.PolicySource{Path: "/repo/profiles/package.use", Line: 4},
	}
	if rule.Source.Line != 4 {
		t.Fatalf("public profile provenance changed: %+v", rule)
	}
}

func TestEffectiveConfigurationPublicContract(t *testing.T) {
	config, err := gentooling.ReadEffectiveConfig(context.Background(), gentooling.SystemPaths{}, gentooling.ConfigOptions{
		Environment: []string{"USE=inspection"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.CommandUse) != 1 || config.CommandUse[0].Name != "inspection" ||
		config.CommandUse[0].Layer != "command" {
		t.Fatalf("public effective configuration changed: %+v", config)
	}
}

func TestAtomAndUseEvaluationPublicContract(t *testing.T) {
	parsed, err := gentooling.ParseAtom(">=sys-kernel/example-2")
	if err != nil {
		t.Fatal(err)
	}
	config := gentooling.EffectiveConfig{
		UserUse: []gentooling.FlagChange{{Name: "modules", Enabled: true, Layer: "user"}},
	}
	id := gentooling.PackageID{Category: "sys-kernel", Name: "example", Version: "2"}
	matched, err := parsed.Matches(id, gentooling.UseState{})
	if err != nil || !matched {
		t.Fatalf("public atom matching = %v, %v", matched, err)
	}
	evaluation, err := config.EvaluateUse(context.Background(), gentooling.PackageContext{
		ID: id, DeclaredUse: []gentooling.UseDeclaration{{Name: "modules"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, found := evaluation.Decision("modules")
	if !found || !decision.Enabled {
		t.Fatalf("public USE evaluation = %+v, found %v", decision, found)
	}
}

func TestSelectionsAndSystemSnapshotPublicContract(t *testing.T) {
	selection := gentooling.Selection{
		Value:  "@world",
		Kind:   gentooling.SetSelection,
		Source: gentooling.PolicySource{Path: "/var/lib/portage/world", Line: 1},
	}
	if selection.Kind != gentooling.SetSelection || selection.Atom != nil {
		t.Fatalf("public selection contract changed: %+v", selection)
	}
	if got := gentooling.PortageStateLockPath("/var/db/pkg"); got != "/var/db/.pkg.portage_lockfile" {
		t.Fatalf("public lock path contract = %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gentooling.ReadSystemSnapshot(ctx, gentooling.SystemPaths{}, gentooling.SnapshotOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("public snapshot cancellation = %v", err)
	}
}

func TestProspectiveVisibilityPublicContract(t *testing.T) {
	config := gentooling.EffectiveConfig{
		Variables: map[string]string{"ARCH": "amd64"},
		PackageKeywords: []gentooling.PackageKeywordRule{{
			Atom:   "sys-kernel/gentoo-sources",
			Source: gentooling.PolicySource{Path: "package.accept_keywords", Line: 1},
		}},
	}
	result, err := config.EvaluateVisibility(context.Background(), gentooling.PackageVisibilityContext{
		ID:       gentooling.PackageID{Category: "sys-kernel", Name: "gentoo-sources", Version: "6.12"},
		Keywords: []string{"~amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Visible || result.Stable || result.Status != gentooling.VisibilityVisible {
		t.Fatalf("public visibility contract = %+v", result)
	}
}

func TestRepositoryDiscoveryAndLocklessSnapshotPublicContract(t *testing.T) {
	root := t.TempDir()
	paths := gentooling.DefaultSystemPaths(root)
	if paths.ReposConf == "" {
		t.Fatal("default repos.conf path is empty")
	}
	repositories, err := gentooling.ReadRepositories(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 0 {
		t.Fatalf("missing repos.conf discovered repositories: %+v", repositories)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = gentooling.ReadSystemSnapshot(ctx, paths, gentooling.SnapshotOptions{
		Consistency: gentooling.StabilizedLockless,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lockless snapshot cancellation = %v", err)
	}
}

func TestRepositoryCandidatePublicContract(t *testing.T) {
	candidate := gentooling.RepositoryCandidate{
		ID:          gentooling.PackageID{Category: "sys-kernel", Name: "gentoo-sources", Version: "6.12", Repository: "gentoo"},
		EAPI:        "8",
		Keywords:    []string{"amd64"},
		DeclaredUse: []gentooling.UseDeclaration{{Name: "experimental", Default: gentooling.UseDefaultDisabled}},
	}
	if candidate.ID.Repository != "gentoo" || candidate.DeclaredUse[0].String() != "-experimental" {
		t.Fatalf("public candidate contract = %+v", candidate)
	}
}

func TestKernelConsumerPublicContracts(t *testing.T) {
	requirement := gentooling.KernelConfigRequirement{
		Symbol: "MODULES", Expectation: gentooling.KernelConfigEnabled,
		Severity:   gentooling.KernelRequirementFatal,
		Conditions: []gentooling.UseCondition{{Flag: "modules", Enabled: true}},
	}
	if requirement.Symbol != "MODULES" || !requirement.Conditions[0].Enabled {
		t.Fatalf("kernel requirement contract = %+v", requirement)
	}
	evaluation := gentooling.KernelRequirementContext{
		Phase: "pkg_setup", KernelRelease: "7.1.5", Architecture: "amd64",
		MergeType: gentooling.MergeSource, EffectiveUSE: []string{"modules"},
	}
	evaluated := gentooling.EvaluatedKernelRequirements{
		Complete: true,
		Requirements: []gentooling.EvaluatedKernelRequirement{{
			Symbol: "MODULES", Applicability: gentooling.Applicable,
		}},
		Unresolved: []gentooling.UnresolvedKernelRequirement{{
			Applicability: gentooling.Inapplicable, Severity: gentooling.KernelRequirementWarning,
		}},
	}
	if evaluation.KernelRelease != "7.1.5" || !evaluated.Complete || evaluated.Requirements[0].Applicability != gentooling.Applicable {
		t.Fatalf("evaluated kernel requirement contract = %+v, %+v", evaluation, evaluated)
	}
	module := gentooling.InstalledKernelModulePackage{
		Package: gentooling.PackageID{Category: "sys-fs", Name: "zfs-kmod", Version: "2.3"},
		Rebuild: gentooling.KernelModuleTargetMissing, NeedsRebuild: true,
	}
	if module.Rebuild.Validate() != nil || !module.NeedsRebuild {
		t.Fatalf("module state contract = %+v", module)
	}
	if !errors.Is(gentooling.ErrCandidateNotFound, gentooling.ErrCandidateNotFound) {
		t.Fatal("candidate error contract changed")
	}
}
