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

func TestV010ChangelogRecordsIntegrityContract(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	changelog := string(data)
	for _, required := range []string{
		"v0.1.0",
		"Concurrent, deterministic installed-package inventory",
		"concurrently changing VDB evidence",
		"Structured IUSE declarations",
		"opt-in CONTENTS",
	} {
		if !strings.Contains(changelog, required) {
			t.Errorf("v0.1.0 changelog omits %q", required)
		}
	}
}

func TestV020ChangelogRecordsProfileContract(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	changelog := string(data)
	for _, required := range []string{
		"v0.2.0",
		"active profile graph",
		"package-policy source lines",
		"configured repository roots",
		"profile escapes",
		"context cancellation",
	} {
		if !strings.Contains(changelog, required) {
			t.Errorf("v0.2.0 changelog omits %q", required)
		}
	}
}

func TestReadmeAcknowledgesGentoolkitLineageAndIndependence(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	for _, required := range []string{
		"name Gentooling is inspired by Gentoolkit",
		"Gentoo package-tooling ecosystem",
		"independent project",
		"not affiliated with Gentoolkit",
		"work alongside existing Gentoo tools, not replace them",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README acknowledgment omits %q", required)
		}
	}
}

func TestV030ChangelogRecordsEffectiveConfigurationContract(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	changelog := string(data)
	for _, required := range []string{
		"v0.3.0",
		"effective Portage configuration",
		"explicitly supplied command environment",
		"USE policy provenance",
		"USE_EXPAND",
		"package atom matching",
	} {
		if !strings.Contains(changelog, required) {
			t.Errorf("v0.3.0 changelog omits %q", required)
		}
	}
}
