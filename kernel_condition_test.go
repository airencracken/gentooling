package gentooling

import "testing"

func TestUseConditionExpressionBooleanSemantics(t *testing.T) {
	node, err := parseUseConditionExpression("use opencl || ( use vulkan && use video_cards_nvk )")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		use   map[string]bool
		known bool
		want  Applicability
	}{
		{name: "first arm", use: map[string]bool{"opencl": true}, known: true, want: Applicable},
		{name: "nested arm", use: map[string]bool{"vulkan": true, "video_cards_nvk": true}, known: true, want: Applicable},
		{name: "partial nested arm", use: map[string]bool{"vulkan": true}, known: true, want: Inapplicable},
		{name: "disabled", use: map[string]bool{}, known: true, want: Inapplicable},
		{name: "unknown", known: false, want: Indeterminate},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := node.evaluate(conditionEvaluation{use: test.use, useKnown: test.known}); got != test.want {
				t.Fatalf("evaluate() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUseConditionExpressionNegationAndPrecedence(t *testing.T) {
	node, err := parseUseConditionExpression("! use test || use pgo && use jit")
	if err != nil {
		t.Fatal(err)
	}
	if got := node.evaluate(conditionEvaluation{use: map[string]bool{"test": true, "pgo": true}, useKnown: true}); got != Inapplicable {
		t.Fatalf("precedence evaluation = %q", got)
	}
	if got := node.evaluate(conditionEvaluation{use: map[string]bool{"test": false}, useKnown: true}); got != Applicable {
		t.Fatalf("negated evaluation = %q", got)
	}
}

func TestUseConditionExpressionRejectsArbitraryShell(t *testing.T) {
	for _, expression := range []string{"has_version dev-libs/foo", "use foo; rm x", "$(use foo)", "use foo | use bar", ""} {
		if _, err := parseUseConditionExpression(expression); err == nil {
			t.Fatalf("unsafe expression %q accepted", expression)
		}
	}
}

func TestKernelPredicateUsesExplicitTargetRelease(t *testing.T) {
	node, err := parseUseConditionExpression("kernel_is -ge 5 11 3 && ! kernel_is -lt 4 18")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		release string
		want    Applicability
	}{
		{release: "7.1.5-gentoo", want: Applicable},
		{release: "5.10.9", want: Inapplicable},
		{release: "", want: Indeterminate},
	} {
		if got := node.evaluate(conditionEvaluation{kernelRelease: test.release}); got != test.want {
			t.Errorf("release %q = %q, want %q", test.release, got, test.want)
		}
	}
}

func FuzzUseConditionExpression(f *testing.F) {
	for _, seed := range []string{"use foo", "! use foo", "use foo || use bar", "( use a && use b )", "$(bad)"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		if node, err := parseUseConditionExpression(expression); err == nil {
			_ = node.evaluate(conditionEvaluation{use: map[string]bool{}, useKnown: true})
		}
	})
}
