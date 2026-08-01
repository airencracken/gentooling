package gentooling

import (
	"context"
	"reflect"
	"testing"
)

func TestEvaluateKernelRequirementsTreatsBoundedSelfReferenceAsAppend(t *testing.T) {
	candidate, repositories := kernelRequirementFixture(t, `pkg_setup() {
	CONFIG_CHECK="~NET ~BT"
	if use mesh || use test; then
		CONFIG_CHECK="${CONFIG_CHECK} ~CRYPTO_USER ~CRYPTO_AES"
	fi
	linux-info_pkg_setup
}
`)
	for _, test := range []struct {
		name string
		use  []string
		want map[string]Applicability
	}{
		{name: "mesh active", use: []string{"mesh"}, want: map[string]Applicability{"NET": Applicable, "BT": Applicable, "CRYPTO_USER": Applicable, "CRYPTO_AES": Applicable}},
		{name: "mesh inactive", use: []string{}, want: map[string]Applicability{"NET": Applicable, "BT": Applicable, "CRYPTO_USER": Inapplicable, "CRYPTO_AES": Inapplicable}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", MergeType: MergeSource, EffectiveUSE: test.use})
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]Applicability)
			for _, requirement := range result.Requirements {
				got[requirement.Symbol] = requirement.Applicability
			}
			if !result.Complete || len(result.Unresolved) != 0 || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("bounded self append = %+v; got %#v, want %#v", result, got, test.want)
			}
		})
	}
}

func TestEvaluateKernelRequirementsMergeTypeForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		condition string
		mergeType MergeType
		want      Applicability
	}{
		{name: "bubblewrap source", condition: `${MERGE_TYPE} != buildonly`, mergeType: MergeSource, want: Applicable},
		{name: "bubblewrap buildonly", condition: `${MERGE_TYPE} != buildonly`, mergeType: MergeBuildOnly, want: Inapplicable},
		{name: "chromium source", condition: `"${MERGE_TYPE}" == "source" || "${MERGE_TYPE}" == "binary"`, mergeType: MergeSource, want: Applicable},
		{name: "chromium binary", condition: `"${MERGE_TYPE}" == "source" || "${MERGE_TYPE}" == "binary"`, mergeType: MergeBinary, want: Applicable},
		{name: "chromium buildonly", condition: `"${MERGE_TYPE}" == "source" || "${MERGE_TYPE}" == "binary"`, mergeType: MergeBuildOnly, want: Inapplicable},
	} {
		t.Run(test.name, func(t *testing.T) {
			ebuild := "pkg_setup() {\n\tif [[ " + test.condition + " ]]; then\n\t\tCONFIG_CHECK=\"~USER_NS\"\n\t\tcheck_extra_config\n\tfi\n}\n"
			candidate, repositories := kernelRequirementFixture(t, ebuild)
			result, err := EvaluateKernelRequirements(context.Background(), candidate, repositories, KernelRequirementContext{Phase: "pkg_setup", MergeType: test.mergeType, EffectiveUSE: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Complete || len(result.Requirements) != 1 || result.Requirements[0].Applicability != test.want {
				t.Fatalf("merge evaluation = %+v, want %s", result, test.want)
			}
		})
	}
}

func TestBoundedConfigCheckSelfAppendRejectsAdditionalSelfReference(t *testing.T) {
	if _, ok := boundedConfigCheckSelfAppend(`${CONFIG_CHECK} ~NET ${CONFIG_CHECK}`, "="); ok {
		t.Fatal("multiple self references were accepted")
	}
	if _, ok := boundedConfigCheckSelfAppend(`$(dynamic) ${CONFIG_CHECK}`, "="); ok {
		t.Fatal("non-leading self reference was accepted")
	}
}
