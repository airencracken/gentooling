package gentooling

import (
	"context"
	"reflect"
	"testing"
)

func TestEvaluateKernelRequirementsPythonStaticAssociativeLoop(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `inherit linux-info
declare -rgA PYTHON_KERNEL_CHECKS=(
	["CROSS_MEMORY_ATTACH"]="test_external_inspection"
	["DNOTIFY"]="test_fcntl"
)
pkg_setup() {
	if [[ ${MERGE_TYPE} != binary ]]; then
		if use test || use pgo; then
			local CONFIG_CHECK
			for f in "${!PYTHON_KERNEL_CHECKS[@]}"; do
				CONFIG_CHECK+="~${f} "
			done
			linux-info_pkg_setup
		fi
	fi
}
`)
	candidate.Inherited = []string{"linux-info"}
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{
		Phase: "pkg_setup", MergeType: MergeSource, EffectiveUSE: []string{"test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Requirements))
	for _, requirement := range result.Requirements {
		if requirement.Applicability == Applicable {
			got = append(got, requirement.Symbol)
		}
	}
	want := []string{"CROSS_MEMORY_ATTACH", "DNOTIFY"}
	if !result.Complete || !reflect.DeepEqual(got, want) {
		t.Fatalf("Python kernel checks = %+v; active = %v, want %v", result, got, want)
	}
}

func TestEvaluateKernelRequirementsBinaryMergeSkipsPythonChecks(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	if [[ ${MERGE_TYPE} != binary ]]; then
		CONFIG_CHECK="~DNOTIFY"
		linux-info_pkg_setup
	fi
}
`)
	result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{
		Phase: "pkg_setup", MergeType: MergeBinary, EffectiveUSE: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Requirements) != 1 || result.Requirements[0].Applicability != Inapplicable {
		t.Fatalf("binary merge evaluation = %+v", result)
	}
}
