package gentooling

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestEvaluateUsePrecedenceAndProvenance(t *testing.T) {
	id := PackageID{Category: "sys-kernel", Name: "example", Version: "2", Slot: "0", Repository: "gentoo"}
	config := EffectiveConfig{
		ProfileUse: []FlagChange{{Name: "modules", Enabled: false, Layer: "profile", Source: PolicySource{Path: "make.defaults", Line: 1}}},
		UserUse:    []FlagChange{{Name: "modules", Enabled: true, Layer: "user", Source: PolicySource{Path: "make.conf", Line: 2}}},
		CommandUse: []FlagChange{{Name: "modules", Enabled: false, Layer: "command", Source: PolicySource{Path: "environment", Line: 1}}},
		UserPackageUse: []PackageFlagRule{{
			Atom: ">=sys-kernel/example-2:0::gentoo", Flags: []string{"modules"},
			Source: PolicySource{Path: "package.use/kernel", Line: 4},
		}},
		Profile: &Profile{Layers: []ProfileLayer{{
			Path: "/profiles/default/linux",
			PackageUseForce: []PackageFlagRule{{
				Atom: "sys-kernel/example", Flags: []string{"modules"},
				Source: PolicySource{Path: "/profiles/package.use.force", Line: 8},
			}},
		}}},
	}
	evaluation, err := config.EvaluateUse(context.Background(), PackageContext{
		ID: id, DeclaredUse: []UseDeclaration{
			{Name: "modules", Default: UseDefaultEnabled},
			{Name: "debug"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, found := evaluation.Decision("modules")
	if !found || !decision.Enabled || !decision.Forced {
		t.Fatalf("modules decision = %+v, found %v", decision, found)
	}
	var kinds []string
	for _, evidence := range decision.Evidence {
		kinds = append(kinds, evidence.Kind)
	}
	want := []string{"iuse-default", "profile-use", "user-use", "user-package-use", "command-use", "package-force"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("evidence kinds = %v, want %v", kinds, want)
	}
	if decision.Evidence[len(decision.Evidence)-1].Source.Line != 8 {
		t.Fatalf("force provenance = %+v", decision.Evidence[len(decision.Evidence)-1])
	}
}

func TestEvaluateUseStablePolicyOnlyAppliesToStablePackage(t *testing.T) {
	config := EffectiveConfig{Profile: &Profile{Layers: []ProfileLayer{{
		Path:          "/profile",
		UseStableMask: []string{"lto"},
	}}}}
	contextFor := func(stable bool) PackageContext {
		return PackageContext{
			ID:          PackageID{Category: "cat", Name: "pkg", Version: "1"},
			DeclaredUse: []UseDeclaration{{Name: "lto", Default: UseDefaultEnabled}},
			Stable:      stable,
		}
	}
	unstable, err := config.EvaluateUse(context.Background(), contextFor(false))
	if err != nil {
		t.Fatal(err)
	}
	stable, err := config.EvaluateUse(context.Background(), contextFor(true))
	if err != nil {
		t.Fatal(err)
	}
	unstableLTO, _ := unstable.Decision("lto")
	stableLTO, _ := stable.Decision("lto")
	if !unstableLTO.Enabled || stableLTO.Enabled || !stableLTO.Masked {
		t.Fatalf("unstable=%+v stable=%+v", unstableLTO, stableLTO)
	}
}

func TestEvaluateUseIgnoresUndeclaredPolicyFlagsAndSorts(t *testing.T) {
	config := EffectiveConfig{UserUse: []FlagChange{
		{Name: "missing", Enabled: true},
		{Name: "zeta", Enabled: true},
	}}
	evaluation, err := config.EvaluateUse(context.Background(), PackageContext{
		ID:          PackageID{Category: "cat", Name: "pkg", Version: "1"},
		DeclaredUse: []UseDeclaration{{Name: "zeta"}, {Name: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{evaluation.Decisions[0].Name, evaluation.Decisions[1].Name}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("decision order = %v", got)
	}
	if _, found := evaluation.Decision("missing"); found {
		t.Fatal("undeclared flag was returned")
	}
}

func TestEvaluateUseCancellationAndInvalidPolicyAreTyped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (EffectiveConfig{}).EvaluateUse(ctx, PackageContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	config := EffectiveConfig{UserPackageUse: []PackageFlagRule{{
		Atom: "not-an-atom", Flags: []string{"flag"}, Source: PolicySource{Path: "package.use", Line: 3},
	}}}
	_, err = config.EvaluateUse(context.Background(), PackageContext{
		ID:          PackageID{Category: "cat", Name: "pkg", Version: "1"},
		DeclaredUse: []UseDeclaration{{Name: "flag"}},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid policy error = %v", err)
	}
}
