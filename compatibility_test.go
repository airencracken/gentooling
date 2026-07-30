package gentooling

import (
	"os"
	"strings"
	"testing"
)

func TestPreV1CompatibilityPolicyIsExplicit(t *testing.T) {
	data, err := os.ReadFile("COMPATIBILITY.md")
	if err != nil {
		t.Fatal(err)
	}
	policy := string(data)
	for _, required := range []string{
		"semantic versioning",
		"minor release may make an incompatible API change",
		"Patch releases remain backward compatible",
		"Stable issue codes",
		"`errors.Is` and `errors.As`",
		"Deprecated APIs",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("compatibility policy omits %q", required)
		}
	}
}
