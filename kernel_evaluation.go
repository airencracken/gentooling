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
	Symbol             string
	Expectation        KernelConfigExpectation
	Severity           KernelRequirementSeverity
	Applicability      Applicability
	Conditions         []UseCondition
	Invocation         KernelCheckInvocation
	Source             PolicySource
	Origin             string
	AssignmentOperator string
}

type UnresolvedKernelRequirement struct {
	Applicability Applicability
	Blocking      bool
	Severity      KernelRequirementSeverity
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
		base := kernelEvidenceApplicability(requirement.Conditions, requirement.ConditionExpression, use, useKnown, evaluation.KernelRelease, evaluation.MergeType)
		invocations := invocationsFor(requirement.Function, requirement.Source, set.Invocations, set.Calls, evaluation.Phase)
		if len(invocations) == 0 {
			invocations = []KernelCheckInvocation{{}}
		}
		for _, invocation := range invocations {
			applicability := combineApplicability(base, invocationApplicability(invocation, use, useKnown, evaluation.KernelRelease, evaluation.MergeType))
			if invocation.Function == "" {
				applicability = Inapplicable
			}
			result.Requirements = append(result.Requirements, EvaluatedKernelRequirement{
				Symbol: requirement.Symbol, Expectation: requirement.Expectation, Severity: requirement.Severity,
				Applicability: applicability, Conditions: cloneUseConditions(requirement.Conditions), Invocation: invocation,
				Source: requirement.Source, Origin: requirement.Origin, AssignmentOperator: requirement.AssignmentOperator,
			})
			if applicability == Indeterminate && requirement.Severity == KernelRequirementFatal {
				result.Complete = false
			}
		}
	}
	applyRequirementAssignmentFlow(result.Requirements)
	for _, dynamic := range set.Dynamic {
		base := kernelEvidenceApplicability(dynamic.Conditions, dynamic.ConditionExpression, use, useKnown, evaluation.KernelRelease, evaluation.MergeType)
		invocations := invocationsFor(dynamic.Function, dynamic.Source, set.Invocations, set.Calls, evaluation.Phase)
		if len(invocations) == 0 {
			invocations = []KernelCheckInvocation{{}}
		}
		for _, invocation := range invocations {
			applicability := combineApplicability(base, invocationApplicability(invocation, use, useKnown, evaluation.KernelRelease, evaluation.MergeType))
			if invocation.Function == "" {
				applicability = Inapplicable
			}
			blocking := applicability != Inapplicable && dynamic.Severity == KernelRequirementFatal
			result.Unresolved = append(result.Unresolved, UnresolvedKernelRequirement{
				Applicability: applicability, Blocking: blocking, Severity: dynamic.Severity, Category: "dynamic-shell",
				OperatorText: dynamic.Reason, DeveloperText: dynamic.Expression,
				Conditions: cloneUseConditions(dynamic.Conditions), Invocation: invocation,
				Source: dynamic.Source, Origin: dynamic.Origin,
			})
			if blocking {
				result.Complete = false
			}
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

func applyRequirementAssignmentFlow(requirements []EvaluatedKernelRequirement) {
	groups := make(map[string][]int)
	for index, requirement := range requirements {
		if requirement.Applicability != Applicable {
			continue
		}
		key := fmt.Sprintf("%s:%d:%s", requirement.Invocation.Source.Path, requirement.Invocation.Source.Line, requirement.Invocation.Function)
		groups[key] = append(groups[key], index)
	}
	for _, indices := range groups {
		sort.SliceStable(indices, func(i, j int) bool {
			left, right := requirements[indices[i]], requirements[indices[j]]
			if left.Source.Path != right.Source.Path {
				return left.Source.Path < right.Source.Path
			}
			return left.Source.Line < right.Source.Line
		})
		active := make([]int, 0, len(indices))
		lastResetPath, lastResetLine := "", -1
		for _, index := range indices {
			requirement := requirements[index]
			if requirement.AssignmentOperator == "=" && (requirement.Source.Path != lastResetPath || requirement.Source.Line != lastResetLine) {
				for _, previous := range active {
					requirements[previous].Applicability = Inapplicable
				}
				active = active[:0]
				lastResetPath, lastResetLine = requirement.Source.Path, requirement.Source.Line
			}
			active = append(active, index)
		}
	}
}

func kernelEvidenceApplicability(conditions []UseCondition, expression string, enabled map[string]bool, known bool, kernelRelease string, mergeType MergeType) Applicability {
	legacy := conditionsApplicability(conditions, enabled, known)
	if expression != "" {
		node, err := parseUseConditionExpression(expression)
		if err != nil {
			return Indeterminate
		}
		return combineApplicability(legacy, node.evaluate(conditionEvaluation{use: enabled, useKnown: known, kernelRelease: kernelRelease, mergeType: mergeType}))
	}
	return legacy
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

func invocationsFor(function string, source PolicySource, invocations []KernelCheckInvocation, calls []KernelFunctionCall, phase string) []KernelCheckInvocation {
	var matched []KernelCheckInvocation
	for _, invocation := range invocations {
		if function != "" && invocation.Function != function {
			continue
		}
		if function == invocation.Function && source.Path == invocation.Source.Path && source.Line > invocation.Source.Line {
			continue
		}
		if phase != "" && !kernelFunctionReachable(phase, invocation.Function, calls) &&
			invocation.Function != phase && !strings.HasSuffix(invocation.Function, "_"+phase) {
			continue
		}
		matched = append(matched, invocation)
	}
	return matched
}

func invocationApplicability(invocation KernelCheckInvocation, enabled map[string]bool, known bool, kernelRelease string, mergeType MergeType) Applicability {
	if invocation.Function == "" {
		return Inapplicable
	}
	return kernelEvidenceApplicability(invocation.Conditions, invocation.ConditionExpression, enabled, known, kernelRelease, mergeType)
}

func combineApplicability(left, right Applicability) Applicability {
	if left == Inapplicable || right == Inapplicable {
		return Inapplicable
	}
	if left == Indeterminate || right == Indeterminate {
		return Indeterminate
	}
	return Applicable
}

func kernelFunctionReachable(from, target string, calls []KernelFunctionCall) bool {
	if from == target {
		return true
	}
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) != 0 {
		caller := queue[0]
		queue = queue[1:]
		for _, call := range calls {
			if call.Caller != caller || seen[call.Callee] {
				continue
			}
			if call.Callee == target {
				return true
			}
			seen[call.Callee] = true
			queue = append(queue, call.Callee)
		}
	}
	return false
}
