package gentooling

import (
	"fmt"
	"strings"
	"unicode"
)

type conditionNode interface {
	evaluate(map[string]bool, bool) Applicability
}

type conditionUse struct{ flag string }
type conditionNot struct{ child conditionNode }
type conditionAnd struct{ left, right conditionNode }
type conditionOr struct{ left, right conditionNode }

func (node conditionUse) evaluate(use map[string]bool, known bool) Applicability {
	if !known {
		return Indeterminate
	}
	if use[node.flag] {
		return Applicable
	}
	return Inapplicable
}

func (node conditionNot) evaluate(use map[string]bool, known bool) Applicability {
	return invertApplicability(node.child.evaluate(use, known))
}

func (node conditionAnd) evaluate(use map[string]bool, known bool) Applicability {
	left, right := node.left.evaluate(use, known), node.right.evaluate(use, known)
	if left == Inapplicable || right == Inapplicable {
		return Inapplicable
	}
	if left == Indeterminate || right == Indeterminate {
		return Indeterminate
	}
	return Applicable
}

func (node conditionOr) evaluate(use map[string]bool, known bool) Applicability {
	left, right := node.left.evaluate(use, known), node.right.evaluate(use, known)
	if left == Applicable || right == Applicable {
		return Applicable
	}
	if left == Indeterminate || right == Indeterminate {
		return Indeterminate
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
		if strings.HasPrefix(input[index:], "&&") || strings.HasPrefix(input[index:], "||") {
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
