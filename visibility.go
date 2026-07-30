package gentooling

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KeywordChange is one ordered ACCEPT_KEYWORDS policy operation.
type KeywordChange struct {
	Keyword string
	Enabled bool
	Source  PolicySource
	Layer   string
}

// PackageKeywordRule is one package.accept_keywords entry. An empty Changes
// slice accepts the host testing keyword, matching Portage behavior.
type PackageKeywordRule struct {
	Atom    string
	Changes []string
	Source  PolicySource
}

// PackageMaskRule is one effective package mask or unmask with its rationale.
type PackageMaskRule struct {
	Atom   string
	Source PolicySource
	Reason string
}

// VisibilityStatus is the primary prospective-package visibility outcome.
type VisibilityStatus string

const (
	VisibilityVisible                 VisibilityStatus = "visible"
	VisibilityPackageMasked           VisibilityStatus = "package_masked"
	VisibilityKeywordMasked           VisibilityStatus = "keyword_masked"
	VisibilityUnsupportedArchitecture VisibilityStatus = "unsupported_architecture"
)

// VisibilityEvidence records one policy input relevant to the outcome.
type VisibilityEvidence struct {
	Kind    string
	Value   string
	Enabled bool
	Source  PolicySource
	Layer   string
	Reason  string
}

// PackageVisibilityContext describes a prospective repository package.
type PackageVisibilityContext struct {
	ID       PackageID
	Keywords []string
}

// VisibilityResult is the effective result plus an ordered explanation.
type VisibilityResult struct {
	Package          PackageID
	Visible          bool
	Stable           bool
	Status           VisibilityStatus
	Architecture     string
	PackageKeywords  []string
	AcceptedKeywords []string
	Evidence         []VisibilityEvidence
}

// EvaluateVisibility applies package masks and effective keyword policy to a
// prospective package. Ordinary rejection is a typed result, not an error.
func (config EffectiveConfig) EvaluateVisibility(ctx context.Context, candidate PackageVisibilityContext) (VisibilityResult, error) {
	if err := ctx.Err(); err != nil {
		return VisibilityResult{}, err
	}
	if _, err := ParsePackageID(candidate.ID.CPV()); err != nil {
		return VisibilityResult{}, err
	}
	architecture := config.Variables["ARCH"]
	if architecture == "" || !validKeyword(architecture) {
		return VisibilityResult{}, fmt.Errorf("%w: effective ARCH is empty or invalid", ErrInvalidData)
	}
	result := VisibilityResult{
		Package: candidate.ID, Architecture: architecture,
		Status: VisibilityVisible, Visible: true,
	}
	for _, raw := range candidate.Keywords {
		keyword := strings.TrimPrefix(raw, "-")
		if keyword != "*" && !validKeyword(keyword) {
			return VisibilityResult{}, fmt.Errorf("%w: package keyword %q", ErrInvalidData, raw)
		}
	}
	for _, rule := range config.PackageMasks {
		matched, err := visibilityRuleMatches(rule.Atom, candidate.ID)
		if err != nil {
			return VisibilityResult{}, visibilityRuleError(rule.Atom, rule.Source, err)
		}
		if matched {
			result.Visible, result.Status = false, VisibilityPackageMasked
			result.Evidence = append(result.Evidence, VisibilityEvidence{
				Kind: "package-mask", Value: rule.Atom, Enabled: true,
				Source: rule.Source, Reason: rule.Reason,
			})
		}
	}
	for _, rule := range config.PackageUnmasks {
		matched, err := visibilityRuleMatches(rule.Atom, candidate.ID)
		if err != nil {
			return VisibilityResult{}, visibilityRuleError(rule.Atom, rule.Source, err)
		}
		if matched {
			result.Visible, result.Status = true, VisibilityVisible
			result.Evidence = append(result.Evidence, VisibilityEvidence{
				Kind: "package-unmask", Value: rule.Atom, Enabled: true, Source: rule.Source,
			})
		}
	}

	result.PackageKeywords = applyKeywordTokens(nil, candidate.Keywords)
	accepted := []string{architecture}
	for _, change := range config.AcceptKeywords {
		accepted = applyKeywordChange(accepted, change.Keyword, change.Enabled)
		result.Evidence = append(result.Evidence, VisibilityEvidence{
			Kind: "accept-keywords", Value: change.Keyword, Enabled: change.Enabled,
			Source: change.Source, Layer: change.Layer,
		})
	}
	for _, rule := range config.PackageKeywords {
		matched, err := visibilityRuleMatches(rule.Atom, candidate.ID)
		if err != nil {
			return VisibilityResult{}, visibilityRuleError(rule.Atom, rule.Source, err)
		}
		if !matched {
			continue
		}
		changes := rule.Changes
		if len(changes) == 0 {
			changes = []string{"~" + architecture}
		}
		for _, raw := range changes {
			enabled := !strings.HasPrefix(raw, "-")
			keyword := strings.TrimPrefix(raw, "-")
			accepted = applyKeywordChange(accepted, keyword, enabled)
			result.Evidence = append(result.Evidence, VisibilityEvidence{
				Kind: "package-accept-keywords", Value: keyword, Enabled: enabled,
				Source: rule.Source, Layer: "package",
			})
		}
	}
	result.AcceptedKeywords = accepted
	result.Stable = containsString(result.PackageKeywords, architecture)
	keywordAccepted := keywordPolicyAccepts(result.PackageKeywords, accepted)
	if result.Visible && !keywordAccepted {
		result.Visible = false
		if hasArchitectureKeyword(result.PackageKeywords, architecture) {
			result.Status = VisibilityKeywordMasked
		} else {
			result.Status = VisibilityUnsupportedArchitecture
		}
	}
	return result, nil
}

func visibilityRuleMatches(raw string, id PackageID) (bool, error) {
	parsed, err := ParseAtom(raw)
	if err != nil {
		return false, err
	}
	return parsed.Matches(id, UseState{})
}

func visibilityRuleError(atom string, source PolicySource, err error) error {
	return fmt.Errorf("%w: visibility atom %q at %s:%d: %v", ErrInvalidData, atom, source.Path, source.Line, err)
}

func keywordPolicyAccepts(keywords, accepted []string) bool {
	for _, allow := range accepted {
		if allow == "**" {
			return true
		}
		for _, keyword := range keywords {
			if allow == keyword ||
				allow == "*" && !strings.HasPrefix(keyword, "~") ||
				allow == "~*" && strings.HasPrefix(keyword, "~") {
				return true
			}
		}
	}
	return false
}

func hasArchitectureKeyword(keywords []string, architecture string) bool {
	return containsString(keywords, architecture) || containsString(keywords, "~"+architecture)
}

func applyKeywordTokens(previous, changes []string) []string {
	result := append([]string(nil), previous...)
	for _, raw := range changes {
		enabled := !strings.HasPrefix(raw, "-")
		result = applyKeywordChange(result, strings.TrimPrefix(raw, "-"), enabled)
	}
	return result
}

func applyKeywordChange(previous []string, keyword string, enabled bool) []string {
	if keyword == "-*" {
		keyword, enabled = "*", false
	}
	if !enabled {
		if keyword == "*" {
			return nil
		}
		result := previous[:0]
		for _, existing := range previous {
			if existing != keyword {
				result = append(result, existing)
			}
		}
		return result
	}
	if !containsString(previous, keyword) {
		return append(previous, keyword)
	}
	return previous
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validKeyword(value string) bool {
	value = strings.TrimPrefix(value, "~")
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func configKeywordChanges(assignments []configValue) []KeywordChange {
	var result []KeywordChange
	for _, assignment := range assignments {
		if assignment.name != "ACCEPT_KEYWORDS" {
			continue
		}
		for _, raw := range strings.Fields(assignment.value) {
			result = append(result, keywordChange(raw, assignment.source, assignment.layer))
		}
	}
	return result
}

func keywordChange(raw string, source PolicySource, layer string) KeywordChange {
	return KeywordChange{
		Keyword: strings.TrimPrefix(raw, "-"), Enabled: !strings.HasPrefix(raw, "-"),
		Source: source, Layer: layer,
	}
}

func loadVisibilityPolicy(ctx context.Context, paths SystemPaths, config *EffectiveConfig) error {
	if paths.ConfigRoot != "" {
		rules, err := readPackageKeywordRules(ctx, filepath.Join(paths.ConfigRoot, "package.accept_keywords"))
		if err != nil {
			return err
		}
		config.PackageKeywords = rules
	}
	var masks, unmasks []PackageMaskRule
	for _, repository := range paths.Repositories {
		rules, err := readPackageMaskRules(ctx, filepath.Join(repository.Path, "profiles", "package.mask"))
		if err != nil {
			return err
		}
		masks = applyPackageMaskChanges(masks, rules)
	}
	if config.Profile != nil {
		for _, directory := range config.Profile.Directories {
			rules, err := readPackageMaskRules(ctx, filepath.Join(directory, "package.mask"))
			if err != nil {
				return err
			}
			masks = applyPackageMaskChanges(masks, rules)
			rules, err = readPackageMaskRules(ctx, filepath.Join(directory, "package.unmask"))
			if err != nil {
				return err
			}
			unmasks = applyPackageMaskChanges(unmasks, rules)
		}
	}
	if paths.ConfigRoot != "" {
		rules, err := readPackageMaskRules(ctx, filepath.Join(paths.ConfigRoot, "package.mask"))
		if err != nil {
			return err
		}
		masks = applyPackageMaskChanges(masks, rules)
		rules, err = readPackageMaskRules(ctx, filepath.Join(paths.ConfigRoot, "package.unmask"))
		if err != nil {
			return err
		}
		unmasks = applyPackageMaskChanges(unmasks, rules)
	}
	config.PackageMasks, config.PackageUnmasks = masks, unmasks
	return nil
}

func readPackageKeywordRules(ctx context.Context, path string) ([]PackageKeywordRule, error) {
	files, err := visibilityPolicyFiles(path)
	if err != nil {
		return nil, err
	}
	var result []PackageKeywordRule
	for _, file := range files {
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			return nil, readErr
		}
		for index, raw := range strings.Split(string(data), "\n") {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if _, parseErr := ParseAtom(fields[0]); parseErr != nil {
				return nil, fmt.Errorf("%w: package keyword atom at %s:%d", ErrInvalidData, file, index+1)
			}
			result = append(result, PackageKeywordRule{
				Atom: fields[0], Changes: append([]string(nil), fields[1:]...),
				Source: PolicySource{Path: file, Line: index + 1},
			})
		}
	}
	return result, nil
}

type maskChange struct {
	rule   PackageMaskRule
	remove bool
}

func readPackageMaskRules(ctx context.Context, path string) ([]maskChange, error) {
	files, err := visibilityPolicyFiles(path)
	if err != nil {
		return nil, err
	}
	var result []maskChange
	for _, file := range files {
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			return nil, readErr
		}
		var reason []string
		for index, raw := range strings.Split(string(data), "\n") {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
			if line == "" {
				reason = nil
				continue
			}
			if strings.HasPrefix(line, "#") {
				reason = append(reason, strings.TrimSpace(strings.TrimPrefix(line, "#")))
				continue
			}
			remove := strings.HasPrefix(line, "-")
			atom := strings.TrimPrefix(line, "-")
			if strings.ContainsAny(atom, " \t") {
				return nil, fmt.Errorf("%w: package mask atom at %s:%d", ErrInvalidData, file, index+1)
			}
			if _, parseErr := ParseAtom(atom); parseErr != nil {
				return nil, fmt.Errorf("%w: package mask atom at %s:%d", ErrInvalidData, file, index+1)
			}
			result = append(result, maskChange{
				rule: PackageMaskRule{
					Atom: atom, Source: PolicySource{Path: file, Line: index + 1},
					Reason: strings.Join(reason, " "),
				},
				remove: remove,
			})
			reason = nil
		}
	}
	return result, nil
}

func visibilityPolicyFiles(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() {
		return []string{path}, nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: policy path %q is not a regular file or directory", ErrInvalidData, path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: policy entry %q is a symlink", ErrInvalidData, entryPath)
		}
		if entry.Type().IsRegular() && !strings.HasPrefix(entry.Name(), ".") && !strings.HasSuffix(entry.Name(), "~") {
			files = append(files, entryPath)
		}
	}
	sort.Strings(files)
	return files, nil
}

func applyPackageMaskChanges(previous []PackageMaskRule, changes []maskChange) []PackageMaskRule {
	result := append([]PackageMaskRule(nil), previous...)
	for _, change := range changes {
		if change.remove {
			filtered := result[:0]
			for _, existing := range result {
				if existing.Atom != change.rule.Atom {
					filtered = append(filtered, existing)
				}
			}
			result = filtered
			continue
		}
		result = append(result, change.rule)
	}
	return result
}

func clonePackageKeywordRules(input []PackageKeywordRule) []PackageKeywordRule {
	result := make([]PackageKeywordRule, len(input))
	for index, rule := range input {
		result[index] = rule
		result[index].Changes = append([]string(nil), rule.Changes...)
	}
	return result
}
