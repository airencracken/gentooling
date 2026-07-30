package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEvaluateVisibilityStableTestingAndUnsupportedArchitecture(t *testing.T) {
	config := EffectiveConfig{
		Variables: map[string]string{"ARCH": "amd64"},
		AcceptKeywords: []KeywordChange{{
			Keyword: "amd64", Enabled: true, Layer: "profile",
			Source: PolicySource{Path: "make.defaults", Line: 2},
		}},
		PackageKeywords: []PackageKeywordRule{{
			Atom: "=dev-lang/go-1.24", Source: PolicySource{Path: "package.accept_keywords", Line: 3},
		}},
	}
	stable, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "dev-lang", Name: "go", Version: "1.23"},
		Keywords: []string{"amd64", "~arm64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stable.Visible || !stable.Stable || stable.Status != VisibilityVisible {
		t.Fatalf("stable result = %+v", stable)
	}

	testingResult, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "dev-lang", Name: "go", Version: "1.24"},
		Keywords: []string{"~amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !testingResult.Visible || testingResult.Stable ||
		!reflect.DeepEqual(testingResult.AcceptedKeywords, []string{"amd64", "~amd64"}) {
		t.Fatalf("testing result = %+v", testingResult)
	}
	if evidence := testingResult.Evidence[len(testingResult.Evidence)-1]; evidence.Kind != "package-accept-keywords" || evidence.Source.Line != 3 {
		t.Fatalf("testing provenance = %+v", evidence)
	}

	unsupported, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "dev-lang", Name: "go", Version: "1.25"},
		Keywords: []string{"arm64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.Visible || unsupported.Status != VisibilityUnsupportedArchitecture {
		t.Fatalf("unsupported result = %+v", unsupported)
	}

	masked, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "dev-lang", Name: "go", Version: "1.25"},
		Keywords: []string{"~amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if masked.Visible || masked.Status != VisibilityKeywordMasked {
		t.Fatalf("keyword mask result = %+v", masked)
	}
}

func TestEvaluateVisibilityMaskUnmaskPrecedenceAndReason(t *testing.T) {
	config := EffectiveConfig{
		Variables: map[string]string{"ARCH": "amd64"},
		PackageMasks: []PackageMaskRule{{
			Atom:   ">=www-client/firefox-150",
			Source: PolicySource{Path: "/repo/profiles/package.mask", Line: 12},
			Reason: "Known security regression.",
		}},
		PackageUnmasks: []PackageMaskRule{{
			Atom:   "=www-client/firefox-152",
			Source: PolicySource{Path: "/etc/portage/package.unmask", Line: 1},
		}},
	}
	result, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "www-client", Name: "firefox", Version: "151"},
		Keywords: []string{"amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Visible || result.Status != VisibilityPackageMasked ||
		result.Evidence[0].Reason != "Known security regression." {
		t.Fatalf("masked result = %+v", result)
	}

	result, err = config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID:       PackageID{Category: "www-client", Name: "firefox", Version: "152"},
		Keywords: []string{"amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Visible || result.Status != VisibilityVisible ||
		result.Evidence[1].Kind != "package-unmask" {
		t.Fatalf("unmasked result = %+v", result)
	}
}

func TestReadEffectiveConfigLoadsVisibilityPolicyInLayerOrder(t *testing.T) {
	paths := effectiveConfigFixture(t)
	repository := paths.Repositories[0].Path
	base, leaf := paths.ActiveProfile, paths.ActiveProfile
	if resolved, err := os.Readlink(paths.ActiveProfile); err == nil {
		leaf = resolved
	}
	base = filepath.Join(repository, "profiles", "base")
	writeProfileFile(t, filepath.Join(repository, "profiles"), "package.mask", "# repository reason\n=cat/pkg-1\n=cat/pkg-2\n")
	writeProfileFile(t, base, "package.mask", "-=cat/pkg-1\n=cat/pkg-3\n")
	writeProfileFile(t, leaf, "package.unmask", "=cat/pkg-2\n")
	writeProfileFile(t, filepath.Join(paths.ConfigRoot, "package.accept_keywords"), "10-local", "cat/pkg\n=cat/pkg-3 -amd64 ~amd64\n")
	writeProfileFile(t, filepath.Join(paths.ConfigRoot, "package.mask"), "local", "=cat/pkg-4\n")
	writeProfileFile(t, filepath.Join(paths.ConfigRoot, "package.unmask"), "local", "=cat/pkg-4\n")

	config, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{
		Environment: []string{"ACCEPT_KEYWORDS=amd64 ~amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if atoms := maskAtoms(config.PackageMasks); !reflect.DeepEqual(atoms, []string{"=cat/pkg-2", "=cat/pkg-3", "=cat/pkg-4"}) {
		t.Fatalf("effective masks = %v", atoms)
	}
	if atoms := maskAtoms(config.PackageUnmasks); !reflect.DeepEqual(atoms, []string{"=cat/pkg-2", "=cat/pkg-4"}) {
		t.Fatalf("effective unmasks = %v", atoms)
	}
	if len(config.PackageKeywords) != 2 || len(config.PackageKeywords[0].Changes) != 0 ||
		config.PackageKeywords[1].Source.Line != 2 {
		t.Fatalf("keyword rules = %+v", config.PackageKeywords)
	}
	if got := config.AcceptKeywords[len(config.AcceptKeywords)-1]; got.Keyword != "~amd64" || got.Layer != "command" {
		t.Fatalf("command keyword provenance = %+v", got)
	}
	if reset := config.AcceptKeywords[len(config.AcceptKeywords)-3]; reset.Keyword != "*" || reset.Enabled {
		t.Fatalf("command keyword reset = %+v", reset)
	}
}

func TestVisibilityPolicyRejectsSymlinksAndMalformedAtoms(t *testing.T) {
	paths := effectiveConfigFixture(t)
	directory := filepath.Join(paths.ConfigRoot, "package.accept_keywords")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(outside, []byte("cat/pkg ~amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("symlink policy error = %v", err)
	}

	config := EffectiveConfig{
		Variables:    map[string]string{"ARCH": "amd64"},
		PackageMasks: []PackageMaskRule{{Atom: "not-an-atom", Source: PolicySource{Path: "mask", Line: 9}}},
	}
	_, err = config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, Keywords: []string{"amd64"},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid atom error = %v", err)
	}
}

func TestEvaluateVisibilityCancellationAndOwnedResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (EffectiveConfig{}).EvaluateVisibility(ctx, PackageVisibilityContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	config := EffectiveConfig{Variables: map[string]string{"ARCH": "amd64"}}
	result, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, Keywords: []string{"amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result.AcceptedKeywords[0] = "mutated"
	again, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, Keywords: []string{"amd64"},
	})
	if err != nil || again.AcceptedKeywords[0] != "amd64" {
		t.Fatalf("result ownership = %+v, %v", again, err)
	}
}

func TestEvaluateVisibilityRejectsMalformedKeywordEvidence(t *testing.T) {
	config := EffectiveConfig{Variables: map[string]string{"ARCH": "amd64"}}
	_, err := config.EvaluateVisibility(context.Background(), PackageVisibilityContext{
		ID: PackageID{Category: "cat", Name: "pkg", Version: "1"}, Keywords: []string{"amd64/escape"},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("malformed keyword error = %v", err)
	}
}

func maskAtoms(rules []PackageMaskRule) []string {
	result := make([]string, len(rules))
	for index, rule := range rules {
		result[index] = rule.Atom
	}
	return result
}
