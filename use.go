package gentooling

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// PackageContext is the package-specific evidence required for USE policy
// evaluation. Stable is explicit because keyword acceptance is consumer policy.
type PackageContext struct {
	ID          PackageID
	DeclaredUse []UseDeclaration
	Stable      bool
}

// UseEvidence records one ordered input to an effective USE decision.
type UseEvidence struct {
	Enabled bool
	Kind    string
	Source  PolicySource
	Layer   string
}

// UseDecision is the effective state and provenance for one declared flag.
type UseDecision struct {
	Name     string
	Enabled  bool
	Default  UseDefault
	Forced   bool
	Masked   bool
	Evidence []UseEvidence
}

// UseEvaluation contains deterministic package-specific USE decisions.
type UseEvaluation struct {
	Package   PackageID
	Decisions []UseDecision
}

// Decision returns a flag decision by name.
func (evaluation UseEvaluation) Decision(name string) (UseDecision, bool) {
	index := sort.Search(len(evaluation.Decisions), func(index int) bool {
		return evaluation.Decisions[index].Name >= name
	})
	if index < len(evaluation.Decisions) && evaluation.Decisions[index].Name == name {
		return evaluation.Decisions[index], true
	}
	return UseDecision{}, false
}

// EvaluateUse computes effective USE for one package. It only returns flags
// declared by IUSE and retains every applied policy input in precedence order.
func (config EffectiveConfig) EvaluateUse(ctx context.Context, packageContext PackageContext) (UseEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return UseEvaluation{}, err
	}
	if _, err := ParsePackageID(packageContext.ID.CPV()); err != nil {
		return UseEvaluation{}, err
	}
	decisions := make(map[string]*UseDecision, len(packageContext.DeclaredUse))
	declared := make(map[string]bool, len(packageContext.DeclaredUse))
	for _, declaration := range packageContext.DeclaredUse {
		if !validUseName(declaration.Name) || declared[declaration.Name] {
			return UseEvaluation{}, fmt.Errorf("%w: invalid or duplicate IUSE declaration %q", ErrInvalidData, declaration.Name)
		}
		declared[declaration.Name] = true
		enabled := declaration.Default == UseDefaultEnabled
		decision := &UseDecision{Name: declaration.Name, Enabled: enabled, Default: declaration.Default}
		if declaration.Default != UseDefaultUnspecified {
			decision.Evidence = append(decision.Evidence, UseEvidence{
				Enabled: enabled, Kind: "iuse-default", Layer: "package",
			})
		}
		decisions[declaration.Name] = decision
	}
	applyChange := func(change FlagChange, kind string) {
		if decision := decisions[change.Name]; decision != nil {
			decision.Enabled = change.Enabled
			decision.Evidence = append(decision.Evidence, UseEvidence{
				Enabled: change.Enabled, Kind: kind, Source: change.Source, Layer: change.Layer,
			})
		}
	}
	for _, change := range config.ProfileUse {
		applyChange(change, "profile-use")
	}
	if config.Profile != nil {
		if err := applyMatchingRules(ctx, config.Profile.PackageUse, packageContext.ID, declared, func(change FlagChange) {
			applyChange(change, "profile-package-use")
		}); err != nil {
			return UseEvaluation{}, err
		}
	}
	for _, change := range config.UserUse {
		applyChange(change, "user-use")
	}
	if err := applyMatchingRules(ctx, config.UserPackageUse, packageContext.ID, declared, func(change FlagChange) {
		applyChange(change, "user-package-use")
	}); err != nil {
		return UseEvaluation{}, err
	}
	for _, change := range config.CommandUse {
		applyChange(change, "command-use")
	}

	if config.Profile != nil {
		for _, layer := range config.Profile.Layers {
			if err := applyForceMaskLayer(ctx, layer, packageContext, declared, decisions); err != nil {
				return UseEvaluation{}, err
			}
		}
	}
	result := UseEvaluation{Package: packageContext.ID, Decisions: make([]UseDecision, 0, len(decisions))}
	for _, decision := range decisions {
		result.Decisions = append(result.Decisions, *decision)
	}
	sort.Slice(result.Decisions, func(left, right int) bool {
		return result.Decisions[left].Name < result.Decisions[right].Name
	})
	return result, nil
}

func applyMatchingRules(ctx context.Context, rules []PackageFlagRule, packageID PackageID, declared map[string]bool, apply func(FlagChange)) error {
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		rawAtom := strings.TrimPrefix(rule.Atom, "-")
		parsed, err := ParseAtom(rawAtom)
		if err != nil {
			return fmt.Errorf("%w: package policy atom %q at %s:%d: %v", ErrInvalidData, rule.Atom, rule.Source.Path, rule.Source.Line, err)
		}
		matched, err := parsed.Matches(packageID, UseState{Declared: declared})
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		for _, raw := range rule.Flags {
			apply(flagChange(raw, rule.Source, "package"))
		}
	}
	return nil
}

func applyForceMaskLayer(ctx context.Context, layer ProfileLayer, packageContext PackageContext, declared map[string]bool, decisions map[string]*UseDecision) error {
	source := func(name string) PolicySource { return PolicySource{Path: layer.Path + "/" + name} }
	apply := func(raw, kind string, enabled, forced, masked bool, policySource PolicySource) {
		name := strings.TrimPrefix(raw, "-")
		decision := decisions[name]
		if decision == nil {
			return
		}
		if strings.HasPrefix(raw, "-") {
			return
		}
		decision.Enabled, decision.Forced, decision.Masked = enabled, decision.Forced || forced, decision.Masked || masked
		decision.Evidence = append(decision.Evidence, UseEvidence{
			Enabled: enabled, Kind: kind, Source: policySource, Layer: "profile",
		})
	}
	for _, raw := range layer.UseForce {
		apply(raw, "profile-force", true, true, false, source("use.force"))
	}
	for _, raw := range layer.UseMask {
		apply(raw, "profile-mask", false, false, true, source("use.mask"))
	}
	if packageContext.Stable {
		for _, raw := range layer.UseStableForce {
			apply(raw, "profile-stable-force", true, true, false, source("use.stable.force"))
		}
		for _, raw := range layer.UseStableMask {
			apply(raw, "profile-stable-mask", false, false, true, source("use.stable.mask"))
		}
	}
	ruleSets := []struct {
		rules      []PackageFlagRule
		kind       string
		enabled    bool
		forced     bool
		masked     bool
		stableOnly bool
	}{
		{layer.PackageUseForce, "package-force", true, true, false, false},
		{layer.PackageUseMask, "package-mask", false, false, true, false},
		{layer.PackageUseStableForce, "package-stable-force", true, true, false, true},
		{layer.PackageUseStableMask, "package-stable-mask", false, false, true, true},
	}
	for _, set := range ruleSets {
		if set.stableOnly && !packageContext.Stable {
			continue
		}
		err := applyMatchingRules(ctx, set.rules, packageContext.ID, declared, func(change FlagChange) {
			apply(change.Name, set.kind, set.enabled, set.forced, set.masked, change.Source)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
