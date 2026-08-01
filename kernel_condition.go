package gentooling

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type conditionNode interface {
	evaluate(conditionEvaluation) Applicability
}

type conditionEvaluation struct {
	use           map[string]bool
	useKnown      bool
	kernelRelease string
	mergeType     MergeType
}

type conditionUse struct{ flag string }
type conditionNot struct{ child conditionNode }
type conditionAnd struct{ left, right conditionNode }
type conditionOr struct{ left, right conditionNode }
type conditionKernel struct {
	operator string
	version  []int
}
type conditionKernelConfigExists struct{}
type conditionMergeType struct {
	operator string
	value    MergeType
}

func (conditionKernelConfigExists) evaluate(evaluation conditionEvaluation) Applicability {
	if evaluation.kernelRelease == "" {
		return Indeterminate
	}
	return Applicable
}
func (node conditionMergeType) evaluate(evaluation conditionEvaluation) Applicability {
	if evaluation.mergeType == "" {
		return Indeterminate
	}
	matched := evaluation.mergeType == node.value
	if node.operator == "!=" {
		matched = !matched
	}
	if matched {
		return Applicable
	}
	return Inapplicable
}

func (node conditionUse) evaluate(evaluation conditionEvaluation) Applicability {
	if !evaluation.useKnown {
		return Indeterminate
	}
	if evaluation.use[node.flag] {
		return Applicable
	}
	return Inapplicable
}

func (node conditionNot) evaluate(evaluation conditionEvaluation) Applicability {
	return invertApplicability(node.child.evaluate(evaluation))
}

func (node conditionAnd) evaluate(evaluation conditionEvaluation) Applicability {
	left, right := node.left.evaluate(evaluation), node.right.evaluate(evaluation)
	if left == Inapplicable || right == Inapplicable {
		return Inapplicable
	}
	if left == Indeterminate || right == Indeterminate {
		return Indeterminate
	}
	return Applicable
}

func (node conditionOr) evaluate(evaluation conditionEvaluation) Applicability {
	left, right := node.left.evaluate(evaluation), node.right.evaluate(evaluation)
	if left == Applicable || right == Applicable {
		return Applicable
	}
	if left == Indeterminate || right == Indeterminate {
		return Indeterminate
	}
	return Inapplicable
}

func (node conditionKernel) evaluate(evaluation conditionEvaluation) Applicability {
	current, ok := parseKernelRelease(evaluation.kernelRelease)
	if !ok {
		return Indeterminate
	}
	comparison := compareKernelVersions(current, node.version)
	matched := map[string]bool{"-lt": comparison < 0, "-le": comparison <= 0, "-eq": comparison == 0, "-ge": comparison >= 0, "-gt": comparison > 0}[node.operator]
	if matched {
		return Applicable
	}
	return Inapplicable
}

func invertApplicability(value Applicability) Applicability {
	switch value {
	case Applicable:
		return Inapplicable
	case Inapplicable:
		return Applicable
	default:
		return Indeterminate
	}
}

type conditionParser struct {
	tokens []string
	index  int
}

func parseUseConditionExpression(input string) (conditionNode, error) {
	parser := conditionParser{tokens: tokenizeCondition(input)}
	if len(parser.tokens) == 0 {
		return nil, fmt.Errorf("%w: empty USE condition", ErrInvalidData)
	}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.index != len(parser.tokens) {
		return nil, fmt.Errorf("%w: unexpected condition token %q", ErrInvalidData, parser.tokens[parser.index])
	}
	return node, nil
}

func tokenizeCondition(input string) []string {
	var tokens []string
	for index := 0; index < len(input); {
		if unicode.IsSpace(rune(input[index])) {
			index++
			continue
		}
		if strings.HasPrefix(input[index:], "&&") || strings.HasPrefix(input[index:], "||") || strings.HasPrefix(input[index:], "==") || strings.HasPrefix(input[index:], "!=") {
			tokens = append(tokens, input[index:index+2])
			index += 2
			continue
		}
		if strings.ContainsRune("()!", rune(input[index])) {
			tokens = append(tokens, input[index:index+1])
			index++
			continue
		}
		start := index
		for index < len(input) && !unicode.IsSpace(rune(input[index])) && !strings.ContainsRune("()!&|", rune(input[index])) {
			index++
		}
		if start == index {
			tokens = append(tokens, input[index:index+1])
			index++
		} else {
			tokens = append(tokens, input[start:index])
		}
	}
	return tokens
}

func (parser *conditionParser) parseOr() (conditionNode, error) {
	left, err := parser.parseAnd()
	for err == nil && parser.accept("||") {
		var right conditionNode
		right, err = parser.parseAnd()
		left = conditionOr{left: left, right: right}
	}
	return left, err
}

func (parser *conditionParser) parseAnd() (conditionNode, error) {
	left, err := parser.parseUnary()
	for err == nil && parser.accept("&&") {
		var right conditionNode
		right, err = parser.parseUnary()
		left = conditionAnd{left: left, right: right}
	}
	return left, err
}

func (parser *conditionParser) parseUnary() (conditionNode, error) {
	if parser.accept("!") {
		child, err := parser.parseUnary()
		return conditionNot{child: child}, err
	}
	if parser.accept("(") {
		node, err := parser.parseOr()
		if err != nil {
			return nil, err
		}
		if !parser.accept(")") {
			return nil, fmt.Errorf("%w: unterminated USE condition group", ErrInvalidData)
		}
		return node, nil
	}
	if parser.accept("kernel_is") {
		if parser.index >= len(parser.tokens) {
			return nil, fmt.Errorf("%w: kernel_is requires a comparison", ErrInvalidData)
		}
		operator := parser.tokens[parser.index]
		parser.index++
		if operator != "-lt" && operator != "-le" && operator != "-eq" && operator != "-ge" && operator != "-gt" {
			return nil, fmt.Errorf("%w: unsupported kernel_is operator %q", ErrInvalidData, operator)
		}
		var version []int
		for parser.index < len(parser.tokens) && parser.tokens[parser.index] != "&&" && parser.tokens[parser.index] != "||" && parser.tokens[parser.index] != ")" {
			part, err := strconv.Atoi(parser.tokens[parser.index])
			if err != nil || part < 0 {
				return nil, fmt.Errorf("%w: invalid kernel_is version component %q", ErrInvalidData, parser.tokens[parser.index])
			}
			version = append(version, part)
			parser.index++
		}
		if len(version) == 0 || len(version) > 4 {
			return nil, fmt.Errorf("%w: kernel_is requires one to four version components", ErrInvalidData)
		}
		return conditionKernel{operator: operator, version: version}, nil
	}
	if parser.accept("linux_config_exists") {
		return conditionKernelConfigExists{}, nil
	}
	if parser.accept("merge_type") {
		if parser.index+1 >= len(parser.tokens) {
			return nil, fmt.Errorf("%w: incomplete merge type predicate", ErrInvalidData)
		}
		operator, value := parser.tokens[parser.index], MergeType(parser.tokens[parser.index+1])
		parser.index += 2
		if operator != "==" && operator != "!=" {
			return nil, fmt.Errorf("%w: unsupported merge type operator %q", ErrInvalidData, operator)
		}
		if value != MergeSource && value != MergeBinary && value != MergeBuildOnly {
			return nil, fmt.Errorf("%w: unsupported merge type %q", ErrInvalidData, value)
		}
		return conditionMergeType{operator: operator, value: value}, nil
	}
	if !parser.accept("use") || parser.index >= len(parser.tokens) {
		return nil, fmt.Errorf("%w: expected bounded 'use FLAG' condition", ErrInvalidData)
	}
	flag := parser.tokens[parser.index]
	parser.index++
	if !validConditionUseFlag(flag) {
		return nil, fmt.Errorf("%w: invalid USE flag %q", ErrInvalidData, flag)
	}
	return conditionUse{flag: flag}, nil
}

func parseKernelRelease(release string) ([]int, bool) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, false
	}
	var result []int
	for _, part := range strings.Split(release, ".") {
		digits := strings.TrimLeftFunc(part, unicode.IsDigit)
		numeric := strings.TrimSuffix(part, digits)
		if numeric == "" {
			break
		}
		value, err := strconv.Atoi(numeric)
		if err != nil {
			return nil, false
		}
		result = append(result, value)
		if digits != "" {
			break
		}
	}
	return result, len(result) != 0
}

func compareKernelVersions(left, right []int) int {
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for index := 0; index < length; index++ {
		var a, b int
		if index < len(left) {
			a = left[index]
		}
		if index < len(right) {
			b = right[index]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func validConditionUseFlag(flag string) bool {
	if flag == "" {
		return false
	}
	for _, current := range flag {
		if unicode.IsLetter(current) || unicode.IsDigit(current) || strings.ContainsRune("+_@-", current) {
			continue
		}
		return false
	}
	return true
}

func (parser *conditionParser) accept(token string) bool {
	if parser.index < len(parser.tokens) && parser.tokens[parser.index] == token {
		parser.index++
		return true
	}
	return false
}
