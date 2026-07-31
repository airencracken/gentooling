package gentooling

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Applicability string

const (
	Applicable    Applicability = "applicable"
	Inapplicable  Applicability = "inapplicable"
	Indeterminate Applicability = "indeterminate"
)

type MergeType string

const (
	MergeSource MergeType = "source"
	MergeBinary MergeType = "binary"
)

type KernelRequirementContext struct {
	Phase         string
	KernelRelease string
	Architecture  string
	MergeType     MergeType
	InstalledUSE  []string
	EffectiveUSE  []string
}

type EvaluatedKernelRequirement struct {
	Symbol        string
	Expectation   KernelConfigExpectation
	Severity      KernelRequirementSeverity
	Applicability Applicability
	Conditions    []UseCondition
	Invocation    KernelCheckInvocation
	Source        PolicySource
	Origin        string
}

type UnresolvedKernelRequirement struct {
	Applicability Applicability
	Blocking      bool
	Category      string
	OperatorText  string
	DeveloperText string
	Conditions    []UseCondition
	Invocation    KernelCheckInvocation
	Source        PolicySource
	Origin        string
}

type EvaluatedKernelRequirements struct {
	Package      PackageID
	Requirements []EvaluatedKernelRequirement
	Unresolved   []UnresolvedKernelRequirement
	Complete     bool
}

func EvaluateKernelRequirements(ctx context.Context, candidate RepositoryCandidate, repositories []Repository, evaluation KernelRequirementContext) (EvaluatedKernelRequirements, error) {
	if evaluation.MergeType != "" && evaluation.MergeType != MergeSource && evaluation.MergeType != MergeBinary {
		return EvaluatedKernelRequirements{}, fmt.Errorf("%w: unknown kernel requirement merge type %q", ErrInvalidData, evaluation.MergeType)
	}
	set, err := ReadKernelRequirements(ctx, candidate, repositories, KernelRequirementOptions{Integrity: AllowPartial})
	if err != nil {
		return EvaluatedKernelRequirements{}, err
	}
	enabled := evaluation.EffectiveUSE
	if enabled == nil {
		enabled = evaluation.InstalledUSE
	}
	useKnown := enabled != nil
	use := make(map[string]bool, len(enabled))
	for _, flag := range enabled {
		use[flag] = true
	}
	result := EvaluatedKernelRequirements{Package: set.Package, Complete: true}
	for _, requirement := range set.Requirements {
		applicability := conditionsApplicability(requirement.Conditions, use, useKnown)
		invocation := invocationFor(requirement.Function, set.Invocations, evaluation.Phase)
		if requirement.Function != "" && invocation.Function == "" {
			applicability = Inapplicable
		}
		result.Requirements = append(result.Requirements, EvaluatedKernelRequirement{
			Symbol: requirement.Symbol, Expectation: requirement.Expectation, Severity: requirement.Severity,
			Applicability: applicability, Conditions: cloneUseConditions(requirement.Conditions),
			Invocation: invocation, Source: requirement.Source, Origin: requirement.Origin,
		})
	}
	for _, dynamic := range set.Dynamic {
		applicability := conditionsApplicability(dynamic.Conditions, use, useKnown)
		invocation := invocationFor(dynamic.Function, set.Invocations, evaluation.Phase)
		if dynamic.Function != "" && invocation.Function == "" {
			applicability = Inapplicable
		}
		blocking := applicability != Inapplicable
		result.Unresolved = append(result.Unresolved, UnresolvedKernelRequirement{
			Applicability: applicability, Blocking: blocking, Category: "dynamic-shell",
			OperatorText: dynamic.Reason, DeveloperText: dynamic.Expression,
			Conditions: cloneUseConditions(dynamic.Conditions), Invocation: invocation,
			Source: dynamic.Source, Origin: dynamic.Origin,
		})
		if blocking {
			result.Complete = false
		}
	}
	sort.SliceStable(result.Unresolved, func(i, j int) bool {
		if result.Unresolved[i].Source.Path != result.Unresolved[j].Source.Path {
			return result.Unresolved[i].Source.Path < result.Unresolved[j].Source.Path
		}
		return result.Unresolved[i].Source.Line < result.Unresolved[j].Source.Line
	})
	return result, nil
}

func conditionsApplicability(conditions []UseCondition, enabled map[string]bool, known bool) Applicability {
	if len(conditions) != 0 && !known {
		return Indeterminate
	}
	for _, condition := range conditions {
		if enabled[condition.Flag] != condition.Enabled {
			return Inapplicable
		}
	}
	return Applicable
}

func invocationFor(function string, invocations []KernelCheckInvocation, phase string) KernelCheckInvocation {
	for _, invocation := range invocations {
		if function != "" && invocation.Function != function {
			continue
		}
		if phase != "" && invocation.Function != phase && !strings.HasSuffix(invocation.Function, "_"+phase) {
			continue
		}
		return invocation
	}
	return KernelCheckInvocation{}
}
