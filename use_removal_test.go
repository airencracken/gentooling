package gentooling

import (
	"context"
	"testing"
)

func TestForceMaskRemovalRestoresOrdinaryUSE(t *testing.T) {
	for _, masked := range []bool{false, true} {
		for _, packageRule := range []bool{false, true} {
			parent, child := ProfileLayer{}, ProfileLayer{}
			if masked {
				parent.UseMask = []string{"flag"}
				child.UseMask = []string{"-flag"}
				if packageRule {
					child.UseMask = nil
					child.PackageUseMask = []PackageFlagRule{{Atom: "cat/pkg", Flags: []string{"-flag"}}}
				}
			} else {
				parent.UseForce = []string{"flag"}
				child.UseForce = []string{"-flag"}
				if packageRule {
					child.UseForce = nil
					child.PackageUseForce = []PackageFlagRule{{Atom: "cat/pkg", Flags: []string{"-flag"}}}
				}
			}
			for _, ordinary := range []bool{false, true} {
				config := EffectiveConfig{
					UserUse: []FlagChange{{Name: "flag", Enabled: ordinary}},
					Profile: &Profile{Layers: []ProfileLayer{parent, child}},
				}
				result, err := config.EvaluateUse(context.Background(), PackageContext{
					ID:          PackageID{Category: "cat", Name: "pkg", Version: "1"},
					DeclaredUse: []UseDeclaration{{Name: "flag"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				decision, _ := result.Decision("flag")
				if decision.Enabled != ordinary || decision.Forced || decision.Masked {
					t.Fatalf("mask=%v package=%v ordinary=%v: %+v", masked, packageRule, ordinary, decision)
				}
			}
		}
	}
}
