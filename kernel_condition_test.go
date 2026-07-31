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
			if got := node.evaluate(test.use, test.known); got != test.want {
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
	if got := node.evaluate(map[string]bool{"test": true, "pgo": true}, true); got != Inapplicable {
		t.Fatalf("precedence evaluation = %q", got)
	}
	if got := node.evaluate(map[string]bool{"test": false}, true); got != Applicable {
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

func FuzzUseConditionExpression(f *testing.F) {
	for _, seed := range []string{"use foo", "! use foo", "use foo || use bar", "( use a && use b )", "$(bad)"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		if node, err := parseUseConditionExpression(expression); err == nil {
			_ = node.evaluate(map[string]bool{}, true)
		}
	})
}
