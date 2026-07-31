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
		applicability := kernelEvidenceApplicability(requirement.Conditions, requirement.ConditionExpression, use, useKnown, evaluation.KernelRelease)
		invocation := invocationFor(requirement.Function, requirement.Source, set.Invocations, set.Calls, evaluation.Phase)
		if requirement.Function != "" && invocation.Function == "" {
			applicability = Inapplicable
		}
		result.Requirements = append(result.Requirements, EvaluatedKernelRequirement{
			Symbol: requirement.Symbol, Expectation: requirement.Expectation, Severity: requirement.Severity,
			Applicability: applicability, Conditions: cloneUseConditions(requirement.Conditions),
			Invocation: invocation, Source: requirement.Source, Origin: requirement.Origin,
			AssignmentOperator: requirement.AssignmentOperator,
		})
	}
	applyRequirementAssignmentFlow(result.Requirements, evaluation.Phase)
	for _, dynamic := range set.Dynamic {
		applicability := kernelEvidenceApplicability(dynamic.Conditions, dynamic.ConditionExpression, use, useKnown, evaluation.KernelRelease)
		invocation := invocationFor(dynamic.Function, dynamic.Source, set.Invocations, set.Calls, evaluation.Phase)
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

func applyRequirementAssignmentFlow(requirements []EvaluatedKernelRequirement, phase string) {
	indices := make([]int, 0, len(requirements))
	for index, requirement := range requirements {
		if requirement.Applicability != Applicable {
			continue
		}
		if requirement.Invocation.Function != "" && phase != "" && requirement.Invocation.Function != phase &&
			!strings.HasSuffix(requirement.Invocation.Function, "_"+phase) {
			continue
		}
		indices = append(indices, index)
	}
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

func kernelEvidenceApplicability(conditions []UseCondition, expression string, enabled map[string]bool, known bool, kernelRelease string) Applicability {
	if expression != "" {
		node, err := parseUseConditionExpression(expression)
		if err != nil {
			return Indeterminate
		}
		return node.evaluate(conditionEvaluation{use: enabled, useKnown: known, kernelRelease: kernelRelease})
	}
	return conditionsApplicability(conditions, enabled, known)
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

func invocationFor(function string, source PolicySource, invocations []KernelCheckInvocation, calls []KernelFunctionCall, phase string) KernelCheckInvocation {
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
		return invocation
	}
	return KernelCheckInvocation{}
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
