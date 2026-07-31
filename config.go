package gentooling

import (
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

type ConfigOptions struct {
	// Environment is command input, not the process environment. Only
	// documented Portage variables and active USE_EXPAND variables are used.
	Environment []string
}

type FlagChange struct {
	Name    string
	Enabled bool
	Source  PolicySource
	Layer   string
}

type EffectiveConfig struct {
	Variables         map[string]string
	Repositories      []Repository
	Profile           *Profile
	ProfileUse        []FlagChange
	UserUse           []FlagChange
	CommandUse        []FlagChange
	UserPackageUse    []PackageFlagRule
	UseExpand         []string
	UseExpandHidden   []string
	UseExpandImplicit []string
	AcceptKeywords    []KeywordChange
	PackageKeywords   []PackageKeywordRule
	PackageMasks      []PackageMaskRule
	PackageUnmasks    []PackageMaskRule
}

type configValue struct {
	name   string
	value  string
	source PolicySource
	layer  string
}

var configReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ReadEffectiveConfig loads make.globals, the active profile graph, user
// make.conf/package.use, and an explicit command environment. It never reads
// the process environment or paths absent from SystemPaths.
func ReadEffectiveConfig(ctx context.Context, paths SystemPaths, options ConfigOptions) (EffectiveConfig, error) {
	if err := ctx.Err(); err != nil {
		return EffectiveConfig{}, err
	}
	var repositories []Repository
	if len(paths.Repositories) == 0 && paths.ReposConf != "" {
		discovered, err := ReadRepositories(ctx, paths)
		if err != nil {
			return EffectiveConfig{}, err
		}
		repositories = discovered
		for _, repository := range discovered {
			paths.Repositories = append(paths.Repositories, RepositoryPath{Name: repository.Name, Path: repository.Location})
		}
	} else {
		for _, repository := range paths.Repositories {
			repositories = append(repositories, Repository{Name: repository.Name, Location: repository.Path})
		}
	}
	values := make(map[string]configValue)
	var allAssignments []configValue
	var profile *Profile
	var err error
	if paths.MakeGlobals != "" {
		assignments, err := readConfigAssignments(ctx, paths.MakeGlobals, true)
		if err != nil {
			return EffectiveConfig{}, fmt.Errorf("load make.globals: %w", err)
		}
		allAssignments = append(allAssignments, assignments...)
		mergeConfigValues(values, assignments)
	}
	if paths.ActiveProfile != "" {
		loaded, err := ReadProfile(ctx, paths)
		if err != nil {
			return EffectiveConfig{}, err
		}
		profile = &loaded
		for _, layer := range loaded.Layers {
			assignments, err := readConfigAssignments(ctx, filepath.Join(layer.Path, "make.defaults"), false)
			if err != nil {
				return EffectiveConfig{}, err
			}
			for index := range assignments {
				assignments[index].layer = "profile"
			}
			allAssignments = append(allAssignments, assignments...)
			mergeConfigValues(values, assignments)
		}
	}
	var userAssignments []configValue
	if paths.ConfigRoot != "" {
		userAssignments, err = readConfigAssignments(ctx, filepath.Join(paths.ConfigRoot, "make.conf"), false)
		if err != nil {
			return EffectiveConfig{}, err
		}
	}
	for index := range userAssignments {
		userAssignments[index].layer = "user"
	}
	allAssignments = append(allAssignments, userAssignments...)
	mergeConfigValues(values, userAssignments)

	result := EffectiveConfig{Variables: make(map[string]string), Repositories: repositories, Profile: profile}
	for name, value := range values {
		result.Variables[name] = value.value
	}
	result.UseExpand = strings.Fields(result.Variables["USE_EXPAND"])
	result.UseExpandHidden = strings.Fields(result.Variables["USE_EXPAND_HIDDEN"])
	result.UseExpandImplicit = strings.Fields(result.Variables["USE_EXPAND_IMPLICIT"])
	result.ProfileUse, result.UserUse = configUseChanges(allAssignments, result.UseExpand)
	if paths.ConfigRoot != "" {
		result.UserPackageUse, err = readConfigPackageRules(ctx, filepath.Join(paths.ConfigRoot, "package.use"))
		if err != nil {
			return EffectiveConfig{}, err
		}
	}
	result.AcceptKeywords = configKeywordChanges(allAssignments)
	if err := applyConfigEnvironment(&result, options.Environment); err != nil {
		return EffectiveConfig{}, err
	}
	if err := loadVisibilityPolicy(ctx, paths, &result); err != nil {
		return EffectiveConfig{}, err
	}
	return cloneEffectiveConfig(result), nil
}

func readConfigAssignments(ctx context.Context, path string, required bool) ([]configValue, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect configuration %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: configuration %q is not a regular file", ErrInvalidData, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read configuration %q: %w", path, err)
	}
	logical, err := logicalConfigLines(string(data), path)
	if err != nil {
		return nil, err
	}
	var result []configValue
	for _, entry := range logical {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(entry.value)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%w: malformed assignment at %s:%d", ErrInvalidData, path, entry.line)
		}
		name = strings.TrimSpace(strings.TrimPrefix(name, "export "))
		if !validConfigName(name) {
			return nil, fmt.Errorf("%w: invalid variable at %s:%d", ErrInvalidData, path, entry.line)
		}
		result = append(result, configValue{
			name:   name,
			value:  stripProfileQuotes(strings.TrimSpace(value)),
			source: PolicySource{Path: path, Line: entry.line},
			layer:  "global",
		})
	}
	return result, nil
}

type logicalConfigLine struct {
	value string
	line  int
}

func logicalConfigLines(data, path string) ([]logicalConfigLine, error) {
	var (
		result []logicalConfigLine
		buffer strings.Builder
		start  int
		quote  byte
		joined bool
	)
	lines := strings.Split(data, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for index, raw := range lines {
		raw = strings.TrimSuffix(raw, "\r")
		if buffer.Len() == 0 {
			if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
				continue
			}
			start = index + 1
		} else if !joined {
			buffer.WriteByte('\n')
		}
		joined = false
		buffer.WriteString(raw)
		var escaped bool
		quote, escaped = configQuoteState(buffer.String())
		if quote != 0 || escaped || configLineContinues(buffer.String()) {
			if quote != '\'' && (escaped || configLineContinues(buffer.String())) {
				current := strings.TrimRight(buffer.String(), " \t")
				buffer.Reset()
				buffer.WriteString(strings.TrimSuffix(current, "\\"))
				joined = true
			}
			continue
		}
		result = append(result, logicalConfigLine{value: buffer.String(), line: start})
		buffer.Reset()
	}
	if buffer.Len() != 0 {
		if quote != 0 {
			return nil, fmt.Errorf("%w: unterminated quote at %s:%d", ErrInvalidData, path, start)
		}
		return nil, fmt.Errorf("%w: unterminated continuation at %s:%d", ErrInvalidData, path, start)
	}
	return result, nil
}

func configQuoteState(value string) (byte, bool) {
	var quote byte
	escaped := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote == 0 && (character == '\'' || character == '"') {
			quote = character
		} else if character == quote {
			quote = 0
		}
	}
	return quote, escaped
}

func configLineContinues(value string) bool {
	trimmed := strings.TrimRight(value, " \t")
	if !strings.HasSuffix(trimmed, "\\") {
		return false
	}
	backslashes := 0
	for index := len(trimmed) - 1; index >= 0 && trimmed[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func mergeConfigValues(destination map[string]configValue, assignments []configValue) {
	for _, assignment := range assignments {
		assignment.value = configReferencePattern.ReplaceAllStringFunc(assignment.value, func(reference string) string {
			match := configReferencePattern.FindStringSubmatch(reference)
			if previous, exists := destination[match[1]]; exists {
				return previous.value
			}
			return ""
		})
		if previous, exists := destination[assignment.name]; exists && incrementalConfigVariable(assignment.name) {
			assignment.value = strings.TrimSpace(previous.value + " " + assignment.value)
		}
		destination[assignment.name] = assignment
	}
}

func configUseChanges(assignments []configValue, expand []string) ([]FlagChange, []FlagChange) {
	var profile, user []FlagChange
	add := func(raw string, source configValue) {
		for _, token := range strings.Fields(raw) {
			change := flagChange(token, source.source, source.layer)
			if source.layer == "user" {
				user = append(user, change)
			} else {
				profile = append(profile, change)
			}
		}
	}
	expandSet := make(map[string]bool, len(expand))
	for _, group := range expand {
		expandSet[group] = true
	}
	for _, value := range assignments {
		switch {
		case value.name == "USE":
			add(value.value, value)
		case expandSet[value.name]:
			var tokens []string
			for _, item := range strings.Fields(value.value) {
				negative := strings.HasPrefix(item, "-")
				name := strings.TrimPrefix(item, "-")
				token := strings.ToLower(value.name) + "_" + name
				if negative {
					token = "-" + token
				}
				tokens = append(tokens, token)
			}
			add(strings.Join(tokens, " "), value)
		}
	}
	return profile, user
}

func applyConfigEnvironment(config *EffectiveConfig, environment []string) error {
	allowed := map[string]bool{
		"USE": true, "ARCH": true, "CHOST": true, "CBUILD": true, "CTARGET": true,
		"CFLAGS": true, "CXXFLAGS": true, "CPPFLAGS": true, "LDFLAGS": true,
		"MAKEOPTS": true, "FEATURES": true, "ACCEPT_KEYWORDS": true,
		"ACCEPT_LICENSE": true,
	}
	for _, group := range config.UseExpand {
		allowed[group] = true
	}
	for index, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || !allowed[name] {
			continue
		}
		config.Variables[name] = value
		source := PolicySource{Path: "environment", Line: index + 1}
		if name == "ACCEPT_KEYWORDS" {
			config.AcceptKeywords = append(config.AcceptKeywords, keywordChange("-*", source, "command"))
			for _, token := range strings.Fields(value) {
				config.AcceptKeywords = append(config.AcceptKeywords, keywordChange(token, source, "command"))
			}
		}
		if name == "USE" {
			for _, token := range strings.Fields(value) {
				config.CommandUse = append(config.CommandUse, flagChange(token, source, "command"))
			}
		}
		for _, group := range config.UseExpand {
			if name != group {
				continue
			}
			for _, token := range strings.Fields(value) {
				negative := strings.HasPrefix(token, "-")
				flag := strings.ToLower(group) + "_" + strings.TrimPrefix(token, "-")
				if negative {
					flag = "-" + flag
				}
				config.CommandUse = append(config.CommandUse, flagChange(flag, source, "command"))
			}
		}
	}
	return nil
}

func readConfigPackageRules(ctx context.Context, path string) ([]PackageFlagRule, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	if info.Mode().IsRegular() {
		files = []string{path}
	} else if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("%w: package.use entry %q is a symlink", ErrInvalidData, filepath.Join(path, entry.Name()))
			}
			if entry.Type().IsRegular() && !strings.HasPrefix(entry.Name(), ".") && !strings.HasSuffix(entry.Name(), "~") {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
		sort.Strings(files)
	} else {
		return nil, fmt.Errorf("%w: package.use %q is not a regular file or directory", ErrInvalidData, path)
	}
	var rules []PackageFlagRule
	for _, file := range files {
		parsed, err := readPackageRules(ctx, file)
		if err != nil {
			return nil, err
		}
		rules = append(rules, parsed...)
	}
	return rules, nil
}

func flagChange(raw string, source PolicySource, layer string) FlagChange {
	return FlagChange{Name: strings.TrimPrefix(raw, "-"), Enabled: !strings.HasPrefix(raw, "-"), Source: source, Layer: layer}
}

func validConfigName(name string) bool {
	if name == "" || !asciiConfigLetter(name[0]) && name[0] != '_' {
		return false
	}
	for _, character := range name[1:] {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func asciiConfigLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func incrementalConfigVariable(name string) bool {
	switch name {
	case "USE", "USE_EXPAND", "USE_EXPAND_HIDDEN", "USE_EXPAND_IMPLICIT", "FEATURES", "ACCEPT_KEYWORDS", "ACCEPT_LICENSE":
		return true
	default:
		return false
	}
}

func cloneEffectiveConfig(input EffectiveConfig) EffectiveConfig {
	input.Variables = cloneStringMap(input.Variables)
	input.Repositories = cloneRepositories(input.Repositories)
	input.ProfileUse = append([]FlagChange(nil), input.ProfileUse...)
	input.UserUse = append([]FlagChange(nil), input.UserUse...)
	input.CommandUse = append([]FlagChange(nil), input.CommandUse...)
	input.UserPackageUse = cloneRules(input.UserPackageUse)
	input.UseExpand = append([]string(nil), input.UseExpand...)
	input.UseExpandHidden = append([]string(nil), input.UseExpandHidden...)
	input.UseExpandImplicit = append([]string(nil), input.UseExpandImplicit...)
	input.AcceptKeywords = append([]KeywordChange(nil), input.AcceptKeywords...)
	input.PackageKeywords = clonePackageKeywordRules(input.PackageKeywords)
	input.PackageMasks = append([]PackageMaskRule(nil), input.PackageMasks...)
	input.PackageUnmasks = append([]PackageMaskRule(nil), input.PackageUnmasks...)
	return input
}
