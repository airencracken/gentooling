package gentooling

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type KernelConfigExpectation uint8

const (
	KernelConfigEnabled KernelConfigExpectation = iota
	KernelConfigDisabled
)

type KernelRequirementSeverity uint8

const (
	KernelRequirementFatal KernelRequirementSeverity = iota
	KernelRequirementWarning
)

type UseCondition struct {
	Flag    string
	Enabled bool
}

type KernelConfigRequirement struct {
	Symbol      string
	Expectation KernelConfigExpectation
	Severity    KernelRequirementSeverity
	Conditions  []UseCondition
	Function    string
	Source      PolicySource
	Origin      string
}

type DynamicKernelEvidence struct {
	Expression string
	Reason     string
	Conditions []UseCondition
	Function   string
	Source     PolicySource
	Origin     string
}

type KernelCheckInvocation struct {
	Function   string
	Conditions []UseCondition
	Source     PolicySource
	Origin     string
}

type KernelRequirementSet struct {
	Package      PackageID
	Requirements []KernelConfigRequirement
	Dynamic      []DynamicKernelEvidence
	Invocations  []KernelCheckInvocation
}

type KernelRequirementOptions struct {
	Integrity IntegrityMode
}

func (requirement KernelConfigRequirement) Validate() error {
	if !kernelSymbolPattern.MatchString(requirement.Symbol) {
		return fmt.Errorf("%w: invalid Kconfig symbol %q", ErrInvalidData, requirement.Symbol)
	}
	if requirement.Expectation != KernelConfigEnabled && requirement.Expectation != KernelConfigDisabled {
		return fmt.Errorf("%w: unknown Kconfig expectation %d", ErrInvalidData, requirement.Expectation)
	}
	if requirement.Severity != KernelRequirementFatal && requirement.Severity != KernelRequirementWarning {
		return fmt.Errorf("%w: unknown kernel requirement severity %d", ErrInvalidData, requirement.Severity)
	}
	for _, condition := range requirement.Conditions {
		if condition.Flag == "" || strings.ContainsAny(condition.Flag, " \t\r\n/") {
			return fmt.Errorf("%w: invalid USE condition %q", ErrInvalidData, condition.Flag)
		}
	}
	return nil
}

var (
	kernelAssignmentPattern = regexp.MustCompile(`\bCONFIG_CHECK\s*(\+?=)\s*(?:"([^"]*)"|'([^']*)')`)
	kernelUsePrefixPattern  = regexp.MustCompile(`^(?:if\s+)?use\s+(!?)([A-Za-z0-9+_@-]+)(?:\s*;\s*then|\s*&&)\s*(.*)$`)
	kernelIfUsePattern      = regexp.MustCompile(`^if\s+use\s+(!?)([A-Za-z0-9+_@-]+)\s*;\s*then\s*$`)
	kernelFunctionPattern   = regexp.MustCompile(`^([A-Za-z0-9+_.-]+)\s*\(\)\s*\{`)
	kernelSymbolPattern     = regexp.MustCompile(`^[A-Z0-9_]+$`)
)

// ReadKernelRequirements extracts conservative static Kconfig evidence from
// an ebuild and its inherited eclasses. Shell is never executed. Any runtime
// check or expression that cannot be represented statically is retained as
// Dynamic evidence for the consumer to resolve or reject.
func ReadKernelRequirements(ctx context.Context, candidate RepositoryCandidate, repositories []Repository, options KernelRequirementOptions) (KernelRequirementSet, error) {
	if options.Integrity != AllowPartial && options.Integrity != RequireComplete {
		return KernelRequirementSet{}, fmt.Errorf("%w: unknown kernel requirement integrity mode %d", ErrInvalidData, options.Integrity)
	}
	if err := ctx.Err(); err != nil {
		return KernelRequirementSet{}, err
	}
	parsedID, err := ParsePackageID(candidate.ID.CPV())
	if err != nil || parsedID.Category != candidate.ID.Category || parsedID.Name != candidate.ID.Name ||
		parsedID.Version != candidate.ID.Version {
		return KernelRequirementSet{}, fmt.Errorf("%w: invalid kernel requirement package identity %q", ErrInvalidData, candidate.ID.CPV())
	}
	repository, found := repositoryByName(repositories, candidate.ID.Repository)
	if !found {
		return KernelRequirementSet{}, fmt.Errorf("%w: repository %q is unavailable for %s", ErrInvalidData, candidate.ID.Repository, candidate.ID.CPV())
	}
	if !validRepositoryName(repository.Name) || !filepath.IsAbs(repository.Location) {
		return KernelRequirementSet{}, fmt.Errorf("%w: invalid repository %q at %q", ErrInvalidData, repository.Name, repository.Location)
	}
	result := KernelRequirementSet{Package: candidate.ID}
	ebuild := filepath.Join(repository.Location, candidate.ID.Category, candidate.ID.Name, candidate.ID.Name+"-"+candidate.ID.Version+".ebuild")
	if err := readKernelRequirementSource(ctx, ebuild, "ebuild", &result); err != nil {
		return KernelRequirementSet{}, err
	}
	for _, inherited := range candidate.Inherited {
		if inherited == "linux-info" {
			continue
		}
		path, origin, err := findInheritedEclass(inherited, repository, repositories)
		if err != nil {
			evidence := DynamicKernelEvidence{
				Expression: "inherit " + inherited, Reason: err.Error(),
				Source: PolicySource{Path: ebuild}, Origin: "ebuild",
			}
			result.Dynamic = append(result.Dynamic, evidence)
			continue
		}
		if err := readKernelRequirementSource(ctx, path, origin, &result); err != nil {
			return KernelRequirementSet{}, err
		}
	}
	sortKernelRequirementSet(&result)
	if options.Integrity == RequireComplete && len(result.Dynamic) != 0 {
		issues := make([]Issue, 0, len(result.Dynamic))
		for _, dynamic := range result.Dynamic {
			id := result.Package
			issues = append(issues, Issue{
				Code: IssueDynamicKernelPolicy, Path: dynamic.Source.Path, Package: &id,
				Message: dynamic.Reason + ": " + dynamic.Expression, Cause: ErrIncompleteEvidence,
			})
		}
		return result, &IntegrityError{Issues: issues}
	}
	return result, nil
}

func repositoryByName(repositories []Repository, name string) (Repository, bool) {
	for _, repository := range repositories {
		if repository.Name == name {
			return repository, true
		}
	}
	return Repository{}, false
}

func findInheritedEclass(name string, repository Repository, repositories []Repository) (string, string, error) {
	search := []Repository{repository}
	for _, master := range repository.Masters {
		if candidate, found := repositoryByName(repositories, master); found {
			search = append(search, candidate)
		}
	}
	for _, candidate := range search {
		path := filepath.Join(candidate.Location, "eclass", name+".eclass")
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() {
			return path, "eclass:" + name, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("inspect eclass %q: %w", path, err)
		}
	}
	return "", "", fmt.Errorf("%w: inherited eclass %q was not found", ErrIncompleteEvidence, name)
}

func readKernelRequirementSource(ctx context.Context, path, origin string, result *KernelRequirementSet) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect kernel requirement source %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: kernel requirement source %q is not a regular file", ErrInvalidData, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read kernel requirement source %q: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNumber := 0
	function := ""
	var conditions []UseCondition
	var controlStack []bool
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return err
		}
		startLine := lineNumber
		raw := scanner.Text()
		for strings.Contains(raw, "CONFIG_CHECK") && shellQuoteOpen(raw) {
			if !scanner.Scan() {
				break
			}
			lineNumber++
			raw += "\n" + scanner.Text()
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if match := kernelFunctionPattern.FindStringSubmatch(line); len(match) != 0 {
			function = match[1]
			conditions = nil
			controlStack = nil
			continue
		}
		if line == "}" {
			function = ""
			conditions = nil
			controlStack = nil
			continue
		}
		if match := kernelIfUsePattern.FindStringSubmatch(line); len(match) != 0 {
			conditions = append(conditions, UseCondition{Flag: match[2], Enabled: match[1] == ""})
			controlStack = append(controlStack, true)
			continue
		}
		if strings.HasPrefix(line, "if ") && strings.HasSuffix(line, "then") {
			controlStack = append(controlStack, false)
			continue
		}
		if strings.HasPrefix(line, "case ") && strings.HasSuffix(line, " in") ||
			(strings.HasPrefix(line, "for ") || strings.HasPrefix(line, "while ") || strings.HasPrefix(line, "until ")) &&
				strings.HasSuffix(line, " do") {
			controlStack = append(controlStack, false)
			continue
		}
		if line == "else" {
			if len(controlStack) != 0 && controlStack[len(controlStack)-1] && len(conditions) != 0 {
				conditions[len(conditions)-1].Enabled = !conditions[len(conditions)-1].Enabled
			}
			continue
		}
		if strings.HasPrefix(line, "elif ") {
			if len(controlStack) != 0 && controlStack[len(controlStack)-1] && len(conditions) != 0 {
				conditions = conditions[:len(conditions)-1]
				controlStack[len(controlStack)-1] = false
			}
			continue
		}
		if line == "fi" {
			if len(controlStack) != 0 && controlStack[len(controlStack)-1] && len(conditions) != 0 {
				conditions = conditions[:len(conditions)-1]
			}
			if len(controlStack) != 0 {
				controlStack = controlStack[:len(controlStack)-1]
			}
			continue
		}
		if line == "esac" || line == "done" {
			if len(controlStack) != 0 {
				controlStack = controlStack[:len(controlStack)-1]
			}
			continue
		}
		lineConditions := append([]UseCondition(nil), conditions...)
		if match := kernelUsePrefixPattern.FindStringSubmatch(line); len(match) != 0 {
			lineConditions = append(lineConditions, UseCondition{Flag: match[2], Enabled: match[1] == ""})
			line = strings.TrimSpace(match[3])
		}
		source := PolicySource{Path: path, Line: startLine}
		if strings.Contains(line, "check_extra_config") || strings.Contains(line, "linux-info_pkg_setup") {
			result.Invocations = append(result.Invocations, KernelCheckInvocation{
				Function: function, Conditions: cloneUseConditions(lineConditions), Source: source, Origin: origin,
			})
		}
		if !strings.Contains(line, "CONFIG_CHECK") {
			continue
		}
		match := kernelAssignmentPattern.FindStringSubmatch(line)
		if len(match) == 0 || dynamicKernelControl(controlStack) {
			reason := "CONFIG_CHECK expression requires shell evaluation"
			if dynamicKernelControl(controlStack) {
				reason = "CONFIG_CHECK is guarded by unsupported shell control flow"
			}
			result.Dynamic = append(result.Dynamic, DynamicKernelEvidence{
				Expression: strings.TrimSpace(raw), Reason: reason,
				Conditions: cloneUseConditions(lineConditions), Function: function, Source: source, Origin: origin,
			})
			continue
		}
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if strings.ContainsAny(value, "$`(){}") {
			result.Dynamic = append(result.Dynamic, DynamicKernelEvidence{
				Expression: strings.TrimSpace(raw), Reason: "CONFIG_CHECK value contains a dynamic shell expression",
				Conditions: cloneUseConditions(lineConditions), Function: function, Source: source, Origin: origin,
			})
			continue
		}
		for _, token := range strings.Fields(value) {
			requirement, ok := parseKernelRequirementToken(token, source, origin, function, lineConditions)
			if !ok {
				result.Dynamic = append(result.Dynamic, DynamicKernelEvidence{
					Expression: token, Reason: "CONFIG_CHECK token is not a static Kconfig symbol",
					Conditions: cloneUseConditions(lineConditions), Function: function, Source: source, Origin: origin,
				})
				continue
			}
			result.Requirements = append(result.Requirements, requirement)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read kernel requirement source %q: %w", path, err)
	}
	return nil
}

func shellQuoteOpen(value string) bool {
	var quote rune
	escaped := false
	for _, current := range value {
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote == 0 && (current == '\'' || current == '"') {
			quote = current
			continue
		}
		if current == quote {
			quote = 0
		}
	}
	return quote != 0
}

func parseKernelRequirementToken(token string, source PolicySource, origin, function string, conditions []UseCondition) (KernelConfigRequirement, bool) {
	requirement := KernelConfigRequirement{
		Expectation: KernelConfigEnabled, Severity: KernelRequirementFatal,
		Conditions: cloneUseConditions(conditions), Function: function, Source: source, Origin: origin,
	}
	if strings.HasPrefix(token, "~") {
		requirement.Severity = KernelRequirementWarning
		token = strings.TrimPrefix(token, "~")
	}
	if strings.HasPrefix(token, "!") {
		requirement.Expectation = KernelConfigDisabled
		token = strings.TrimPrefix(token, "!")
	}
	token = strings.TrimPrefix(token, "CONFIG_")
	if !kernelSymbolPattern.MatchString(token) {
		return KernelConfigRequirement{}, false
	}
	requirement.Symbol = token
	return requirement, true
}

func dynamicKernelControl(stack []bool) bool {
	for _, supported := range stack {
		if !supported {
			return true
		}
	}
	return false
}

func cloneUseConditions(input []UseCondition) []UseCondition {
	return append([]UseCondition(nil), input...)
}

func sortKernelRequirementSet(result *KernelRequirementSet) {
	sort.SliceStable(result.Requirements, func(i, j int) bool {
		left, right := result.Requirements[i], result.Requirements[j]
		if left.Symbol != right.Symbol {
			return left.Symbol < right.Symbol
		}
		if left.Source.Path != right.Source.Path {
			return left.Source.Path < right.Source.Path
		}
		return left.Source.Line < right.Source.Line
	})
	sort.SliceStable(result.Dynamic, func(i, j int) bool {
		if result.Dynamic[i].Source.Path != result.Dynamic[j].Source.Path {
			return result.Dynamic[i].Source.Path < result.Dynamic[j].Source.Path
		}
		return result.Dynamic[i].Source.Line < result.Dynamic[j].Source.Line
	})
	sort.SliceStable(result.Invocations, func(i, j int) bool {
		if result.Invocations[i].Source.Path != result.Invocations[j].Source.Path {
			return result.Invocations[i].Source.Path < result.Invocations[j].Source.Path
		}
		return result.Invocations[i].Source.Line < result.Invocations[j].Source.Line
	})
}
