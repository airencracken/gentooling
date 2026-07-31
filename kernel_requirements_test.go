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
	if len(result.Requirements) != 5 || len(result.Dynamic) != 0 || len(result.Invocations) != 1 {
		t.Fatalf("kernel requirements = %+v", result)
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

func TestReadKernelRequirementsParsesMultilineAndLocalAssignments(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `inherit linux-info
pkg_setup() {
	local CONFIG_CHECK="
		~TIMERFD
		~EVENTFD
	"
	CONFIG_CHECK+=" ~CRYPTO_USER_API"
	linux-info_pkg_setup
}
`)
	candidate.Inherited = []string{"linux-info"}
	result, err := ReadKernelRequirements(context.Background(), candidate, repositories, KernelRequirementOptions{Integrity: RequireComplete})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 3 || len(result.Dynamic) != 0 || len(result.Invocations) != 1 {
		t.Fatalf("multiline kernel requirements = %+v", result)
	}
	if result.Requirements[1].Source.Line != 3 || result.Requirements[2].Source.Line != 3 || result.Invocations[0].Source.Line != 8 {
		t.Fatalf("multiline provenance = %+v, %+v", result.Requirements, result.Invocations)
	}
}

func TestEvaluateKernelRequirementsClassifiesInactiveDynamicEvidence(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `inherit linux-info
pkg_setup() {
	if use test ; then
		CONFIG_CHECK="${DYNAMIC_CHECKS}"
	fi
	CONFIG_CHECK="~SYSVIPC"
	linux-info_pkg_setup
}

`)
	candidate.Inherited = []string{"linux-info"}
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{
		Phase: "pkg_setup", EffectiveUSE: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Unresolved) != 1 || result.Unresolved[0].Applicability != Inapplicable || result.Unresolved[0].Blocking {
		t.Fatalf("inactive dynamic evidence = %+v", result)
	}
	if len(result.Requirements) != 1 || result.Requirements[0].Symbol != "SYSVIPC" || result.Requirements[0].Applicability != Applicable {
		t.Fatalf("evaluated requirements = %+v", result.Requirements)
	}

	unknown, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup"})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Complete || unknown.Unresolved[0].Applicability != Indeterminate || !unknown.Unresolved[0].Blocking {
		t.Fatalf("indeterminate dynamic evidence = %+v", unknown)
	}
}

func TestEvaluateKernelRequirementsBooleanIfElifElse(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	if use test || use pgo ; then
		CONFIG_CHECK="~TEST_PATH"
	elif use opencl || ( use vulkan && use video_cards_nvk ) ; then
		CONFIG_CHECK="~GPU_PATH"
	else
		CONFIG_CHECK="~PLAIN_PATH"
	fi
	check_extra_config
}

`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{
		Phase: "pkg_setup", EffectiveUSE: []string{"vulkan", "video_cards_nvk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	applicability := make(map[string]Applicability)
	for _, requirement := range result.Requirements {
		applicability[requirement.Symbol] = requirement.Applicability
	}
	if applicability["TEST_PATH"] != Inapplicable || applicability["GPU_PATH"] != Applicable || applicability["PLAIN_PATH"] != Inapplicable {
		t.Fatalf("boolean branch applicability = %#v; result = %+v", applicability, result)
	}
}

func TestEvaluateKernelRequirementsAssignmentReplacementAndAppend(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	CONFIG_CHECK="~INOTIFY_USER"
	CONFIG_CHECK+=" ~TIMERFD"
	if use test ; then
		CONFIG_CHECK="~IPV6"
		CONFIG_CHECK+=" ~KUNIT"
	fi
	linux-info_pkg_setup
}
`)
	for _, test := range []struct {
		name string
		use  []string
		want map[string]Applicability
	}{
		{name: "replacement", use: []string{"test"}, want: map[string]Applicability{
			"INOTIFY_USER": Inapplicable, "TIMERFD": Inapplicable, "IPV6": Applicable, "KUNIT": Applicable,
		}},
		{name: "inactive replacement", use: []string{}, want: map[string]Applicability{
			"INOTIFY_USER": Applicable, "TIMERFD": Applicable, "IPV6": Inapplicable, "KUNIT": Inapplicable,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", EffectiveUSE: test.use})
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]Applicability)
			for _, requirement := range result.Requirements {
				got[requirement.Symbol] = requirement.Applicability
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("assignment flow = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestEvaluateKernelRequirementsUsesExplicitKernelRelease(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	if kernel_is -ge 6 12 ; then
		CONFIG_CHECK="MODVERSIONS"
	fi
	if use feature ; then
		CONFIG_CHECK="FEATURE_A"
	else
		CONFIG_CHECK="FEATURE_B"
	fi
	check_extra_config
}

`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", KernelRelease: "7.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	bySymbol := make(map[string]Applicability)
	for _, requirement := range result.Requirements {
		bySymbol[requirement.Symbol] = requirement.Applicability
	}
	if bySymbol["MODVERSIONS"] != Applicable {
		t.Fatalf("kernel predicate result = %+v", result)
	}
	unknown, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup"})
	if err != nil {
		t.Fatal(err)
	}
	unknownBySymbol := make(map[string]Applicability)
	for _, requirement := range unknown.Requirements {
		unknownBySymbol[requirement.Symbol] = requirement.Applicability
	}
	if unknownBySymbol["MODVERSIONS"] != Indeterminate {
		t.Fatalf("missing target kernel was not indeterminate: %+v", unknown)
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

func TestEvaluateKernelRequirementsStaticLoopsAndWrapper(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `kernel_checks() {
	declare -A checks=( [BPF]=yes [PERF_EVENTS]=yes )
	for symbol in "${!checks[@]}"; do
		CONFIG_CHECK+="~${symbol} "
	done
	check_extra_config
}
pkg_setup() {
	kernel_checks
}
`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", EffectiveUSE: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Applicability)
	for _, requirement := range result.Requirements {
		got[requirement.Symbol] = requirement.Applicability
	}
	if !result.Complete || got["BPF"] != Applicable || got["PERF_EVENTS"] != Applicable || result.Requirements[0].Invocation.Function != "kernel_checks" {
		t.Fatalf("static wrapper evaluation = %+v", result)
	}
}

func TestEvaluateKernelRequirementsDynamicArrayBlocks(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	local checks=( "${DYNAMIC}" )
	for symbol in "${checks[@]}"; do
		CONFIG_CHECK+="~${symbol} "
	done
	linux-info_pkg_setup
}
`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", EffectiveUSE: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || len(result.Unresolved) == 0 || !result.Unresolved[0].Blocking {
		t.Fatalf("dynamic array = %+v", result)
	}
}

func TestEvaluateKernelRequirementsInvocationSnapshot(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	CONFIG_CHECK="~TIMERFD"
	check_extra_config
	CONFIG_CHECK+="~EVENTFD"
}
`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", EffectiveUSE: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Applicability)
	for _, requirement := range result.Requirements {
		got[requirement.Symbol] = requirement.Applicability
	}
	if got["TIMERFD"] != Applicable || got["EVENTFD"] != Inapplicable {
		t.Fatalf("invocation snapshot = %+v", result)
	}
}
