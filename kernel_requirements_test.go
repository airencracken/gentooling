package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func kernelRequirementFixture(t *testing.T, ebuild string) (RepositoryCandidate, []Repository) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "sys-kernel", "example", "example-1.ebuild")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(ebuild), 0o644); err != nil {
		t.Fatal(err)
	}
	return RepositoryCandidate{
		ID: PackageID{Category: "sys-kernel", Name: "example", Version: "1", Repository: "test"},
	}, []Repository{{Name: "test", Location: root}}
}

func TestReadKernelRequirementsExtractsStaticAndConditionalEvidence(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `EAPI=8
inherit linux-info
CONFIG_CHECK="~CONFIG_BPF !DEBUG_INFO MODULES"
pkg_setup() {
	use test && CONFIG_CHECK+=" ~KUNIT"
	if use !minimal ; then
		CONFIG_CHECK+=" PREEMPT"
	fi
	check_extra_config
}
`)
	candidate.Inherited = []string{"linux-info"}
	result, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{Integrity: AllowPartial})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 5 || len(result.Dynamic) != 1 || len(result.Invocations) != 1 {
		t.Fatalf("kernel requirements = %+v", result)
	}
	if result.Dynamic[0].Reason != "linux-info.eclass check dispatch requires runtime phase evaluation" {
		t.Fatalf("linux-info evidence = %+v", result.Dynamic)
	}
	bySymbol := make(map[string]KernelConfigRequirement)
	for _, requirement := range result.Requirements {
		bySymbol[requirement.Symbol] = requirement
	}
	if bySymbol["BPF"].Severity != KernelRequirementWarning ||
		bySymbol["DEBUG_INFO"].Expectation != KernelConfigDisabled ||
		!reflect.DeepEqual(bySymbol["KUNIT"].Conditions, []UseCondition{{Flag: "test", Enabled: true}}) ||
		!reflect.DeepEqual(bySymbol["PREEMPT"].Conditions, []UseCondition{{Flag: "minimal", Enabled: false}}) ||
		bySymbol["PREEMPT"].Function != "pkg_setup" || result.Invocations[0].Function != "pkg_setup" {
		t.Fatalf("structured evidence = %+v", result)
	}
}

func TestReadKernelRequirementsDoesNotMisrepresentUnsupportedControlFlow(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	if kernel_is -ge 6 12 ; then
		CONFIG_CHECK="MODVERSIONS"
	fi
	if use feature ; then
		CONFIG_CHECK="FEATURE_A"
	else
		CONFIG_CHECK="FEATURE_B"
	fi
}
`)
	result, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dynamic) != 1 || result.Dynamic[0].Reason != "CONFIG_CHECK is guarded by unsupported shell control flow" {
		t.Fatalf("unsupported flow = %+v", result)
	}
	bySymbol := make(map[string]KernelConfigRequirement)
	for _, requirement := range result.Requirements {
		bySymbol[requirement.Symbol] = requirement
	}
	if !reflect.DeepEqual(bySymbol["FEATURE_A"].Conditions, []UseCondition{{Flag: "feature", Enabled: true}}) ||
		!reflect.DeepEqual(bySymbol["FEATURE_B"].Conditions, []UseCondition{{Flag: "feature", Enabled: false}}) {
		t.Fatalf("else conditions = %+v", result.Requirements)
	}
}

func TestReadKernelRequirementsPreservesDynamicShellEvidence(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	CONFIG_CHECK="${CONFIG_CHECK} $(dynamic_checks)"
	check_extra_config
}
`)
	partial, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{Integrity: AllowPartial})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Dynamic) != 1 || partial.Dynamic[0].Source.Line != 2 ||
		partial.Dynamic[0].Reason != "CONFIG_CHECK value contains a dynamic shell expression" {
		t.Fatalf("dynamic evidence = %+v", partial.Dynamic)
	}
	strict, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{Integrity: RequireComplete})
	if !errors.Is(err, ErrIncompleteEvidence) || len(strict.Dynamic) != 1 {
		t.Fatalf("strict evidence = %+v, %v", strict, err)
	}
}

func TestReadKernelRequirementsReadsInheritedEclassesFromMasters(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, "inherit module-policy\n")
	master := t.TempDir()
	if err := os.MkdirAll(filepath.Join(master, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	eclass := filepath.Join(master, "eclass", "module-policy.eclass")
	if err := os.WriteFile(eclass, []byte(`CONFIG_CHECK="MODULES ~MODVERSIONS"`), 0o644); err != nil {
		t.Fatal(err)
	}
	repositories[0].Masters = []string{"gentoo"}
	repositories = append(repositories, Repository{Name: "gentoo", Location: master})
	candidate.Inherited = []string{"module-policy"}
	result, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 2 || result.Requirements[0].Origin != "eclass:module-policy" {
		t.Fatalf("eclass evidence = %+v", result)
	}
}

func TestReadKernelRequirementsRejectsUnsafeSourcesAndOptions(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, "EAPI=8\n")
	if _, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{Integrity: IntegrityMode(99)}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("integrity error = %v", err)
	}
	candidate.ID.Repository = "../escape"
	if _, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("repository error = %v", err)
	}
	candidate.ID = PackageID{Category: "..", Name: "example", Version: "1", Repository: "test"}
	if _, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("identity error = %v", err)
	}
}

func TestKernelConfigRequirementSchemaValidation(t *testing.T) {
	valid := KernelConfigRequirement{Symbol: "MODULES", Expectation: KernelConfigEnabled, Severity: KernelRequirementFatal}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []KernelConfigRequirement{
		{Symbol: "../BAD"},
		{Symbol: "MODULES", Expectation: KernelConfigExpectation(99)},
		{Symbol: "MODULES", Severity: KernelRequirementSeverity(99)},
		{Symbol: "MODULES", Conditions: []UseCondition{{Flag: "../../bad"}}},
	} {
		if err := invalid.Validate(); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("schema error for %+v = %v", invalid, err)
		}
	}
}

func FuzzKernelRequirementToken(f *testing.F) {
	for _, seed := range []string{"MODULES", "~BPF", "!DEBUG_INFO", "~!KEXEC", "CONFIG_PREEMPT", "$(bad)", "../../bad"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		requirement, ok := parseKernelRequirementToken(token, PolicySource{}, "fuzz", "", nil)
		if ok && !kernelSymbolPattern.MatchString(requirement.Symbol) {
			t.Fatalf("accepted invalid symbol %q from %q", requirement.Symbol, token)
		}
	})
}
