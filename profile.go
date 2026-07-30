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

var ErrProfileCycle = errors.New("gentooling: profile parent cycle")

type PolicySource struct {
	Path string
	Line int
}

type PackageFlagRule struct {
	Atom   string
	Flags  []string
	Source PolicySource
}

type ProfileLayer struct {
	Path                  string
	Parents               []string
	MakeDefaults          map[string]string
	System                []string
	PackageProvided       []string
	UseForce              []string
	UseMask               []string
	UseStableForce        []string
	UseStableMask         []string
	PackageUse            []PackageFlagRule
	PackageUseForce       []PackageFlagRule
	PackageUseMask        []PackageFlagRule
	PackageUseStableForce []PackageFlagRule
	PackageUseStableMask  []PackageFlagRule
}

type Profile struct {
	ActivePath            string
	Directories           []string
	Layers                []ProfileLayer
	MakeDefaults          map[string]string
	System                []string
	PackageProvided       []string
	UseForce              []string
	UseMask               []string
	UseStableForce        []string
	UseStableMask         []string
	PackageUse            []PackageFlagRule
	PackageUseForce       []PackageFlagRule
	PackageUseMask        []PackageFlagRule
	PackageUseStableForce []PackageFlagRule
	PackageUseStableMask  []PackageFlagRule
}

// ReadProfile loads the active Portage profile in root-to-leaf order. All
// cross-repository parents are resolved from explicit repository paths.
func ReadProfile(ctx context.Context, paths SystemPaths) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if paths.ActiveProfile == "" {
		return Profile{}, fmt.Errorf("%w: active profile path is empty", ErrInvalidData)
	}
	target, err := os.Readlink(paths.ActiveProfile)
	if err != nil {
		return Profile{}, fmt.Errorf("read active profile %q: %w", paths.ActiveProfile, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(paths.ActiveProfile), target)
	}
	target = filepath.Clean(target)
	repositories, err := profileRepositories(target, paths.Repositories)
	if err != nil {
		return Profile{}, err
	}
	if !withinAnyProfileRoot(target, repositories) {
		return Profile{}, fmt.Errorf("%w: active profile %q escapes configured repositories", ErrInvalidData, target)
	}

	var order []string
	layers := make(map[string]ProfileLayer)
	seen, stack := make(map[string]bool), make(map[string]bool)
	if err := collectProfile(ctx, target, repositories, seen, stack, &order, layers); err != nil {
		return Profile{}, err
	}
	result := Profile{ActivePath: target, Directories: append([]string(nil), order...), MakeDefaults: make(map[string]string)}
	for _, directory := range order {
		layer := layers[directory]
		result.Layers = append(result.Layers, cloneProfileLayer(layer))
		mergeProfileLayer(&result, layer)
	}
	return result, nil
}

func collectProfile(ctx context.Context, path string, repositories map[string]string, seen, stack map[string]bool, order *[]string, layers map[string]ProfileLayer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	if stack[path] {
		return fmt.Errorf("%w: %s", ErrProfileCycle, path)
	}
	if seen[path] {
		return nil
	}
	if !withinAnyProfileRoot(path, repositories) {
		return fmt.Errorf("%w: profile %q escapes configured repositories", ErrInvalidData, path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect profile %q: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: profile %q is not a directory", ErrInvalidData, path)
	}
	layer, err := readProfileLayer(ctx, path)
	if err != nil {
		return err
	}
	stack[path] = true
	for _, parent := range layer.Parents {
		resolved, err := resolveProfileParent(path, parent, repositories)
		if err != nil {
			return err
		}
		if err := collectProfile(ctx, resolved, repositories, seen, stack, order, layers); err != nil {
			return err
		}
	}
	delete(stack, path)
	seen[path] = true
	layers[path] = layer
	*order = append(*order, path)
	return nil
}

func readProfileLayer(ctx context.Context, path string) (ProfileLayer, error) {
	layer := ProfileLayer{Path: path, MakeDefaults: make(map[string]string)}
	var err error
	if layer.Parents, err = readPolicyLines(ctx, filepath.Join(path, "parent"), false); err != nil {
		return ProfileLayer{}, err
	}
	if layer.MakeDefaults, err = readAssignments(ctx, filepath.Join(path, "make.defaults")); err != nil {
		return ProfileLayer{}, err
	}
	if layer.System, err = readSystem(ctx, filepath.Join(path, "packages")); err != nil {
		return ProfileLayer{}, err
	}
	if layer.PackageProvided, err = readPolicyLines(ctx, filepath.Join(path, "package.provided"), false); err != nil {
		return ProfileLayer{}, err
	}
	for file, destination := range map[string]*[]string{
		"use.force": &layer.UseForce, "use.mask": &layer.UseMask,
		"use.stable.force": &layer.UseStableForce, "use.stable.mask": &layer.UseStableMask,
	} {
		if *destination, err = readPolicyLines(ctx, filepath.Join(path, file), false); err != nil {
			return ProfileLayer{}, err
		}
	}
	for file, destination := range map[string]*[]PackageFlagRule{
		"package.use": &layer.PackageUse, "package.use.force": &layer.PackageUseForce,
		"package.use.mask":         &layer.PackageUseMask,
		"package.use.stable.force": &layer.PackageUseStableForce,
		"package.use.stable.mask":  &layer.PackageUseStableMask,
	} {
		if *destination, err = readPackageRules(ctx, filepath.Join(path, file)); err != nil {
			return ProfileLayer{}, err
		}
	}
	return layer, nil
}

func readOptionalRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect profile policy %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: profile policy %q is not a regular file", ErrInvalidData, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile policy %q: %w", path, err)
	}
	return data, nil
}

func readPolicyLines(ctx context.Context, path string, keepComments bool) ([]string, error) {
	data, err := readOptionalRegular(path)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, raw := range strings.Split(string(data), "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || !keepComments && strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return result, nil
}

func readAssignments(ctx context.Context, path string) (map[string]string, error) {
	lines, err := readPolicyLines(ctx, path, false)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range lines {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), stripProfileQuotes(strings.TrimSpace(value))
		if name != "" {
			result[name] = value
		}
	}
	return result, nil
}

func readSystem(ctx context.Context, path string) ([]string, error) {
	lines, err := readPolicyLines(ctx, path, false)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "-*"):
			result = append(result, "-"+strings.TrimPrefix(line, "-*"))
		case strings.HasPrefix(line, "*"):
			result = append(result, strings.TrimPrefix(line, "*"))
		}
	}
	return result, nil
}

func readPackageRules(ctx context.Context, path string) ([]PackageFlagRule, error) {
	data, err := readOptionalRegular(path)
	if err != nil {
		return nil, err
	}
	var rules []PackageFlagRule
	for index, raw := range strings.Split(string(data), "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%w: malformed package flag rule at %s:%d", ErrInvalidData, path, index+1)
		}
		rules = append(rules, PackageFlagRule{
			Atom: fields[0], Flags: append([]string(nil), fields[1:]...),
			Source: PolicySource{Path: path, Line: index + 1},
		})
	}
	return rules, nil
}

func resolveProfileParent(current, parent string, repositories map[string]string) (string, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" || filepath.IsAbs(parent) {
		return "", fmt.Errorf("%w: invalid profile parent %q in %s", ErrInvalidData, parent, current)
	}
	if repository, subpath, found := strings.Cut(parent, ":"); found {
		root := repositories[repository]
		if root == "" || subpath == "" || filepath.IsAbs(subpath) {
			return "", fmt.Errorf("%w: invalid cross-repository profile parent %q", ErrInvalidData, parent)
		}
		resolved := filepath.Clean(filepath.Join(root, subpath))
		if !withinRoot(resolved, root) {
			return "", fmt.Errorf("%w: profile parent %q escapes %s", ErrInvalidData, parent, root)
		}
		return resolved, nil
	}
	resolved := filepath.Clean(filepath.Join(current, parent))
	if !withinAnyProfileRoot(resolved, repositories) {
		return "", fmt.Errorf("%w: profile parent %q escapes configured repositories", ErrInvalidData, parent)
	}
	return resolved, nil
}

func profileRepositories(active string, configured []RepositoryPath) (map[string]string, error) {
	result := make(map[string]string)
	for _, repository := range configured {
		if repository.Name == "" || repository.Path == "" {
			return nil, fmt.Errorf("%w: repository name and path are required", ErrInvalidData)
		}
		result[repository.Name] = filepath.Join(filepath.Clean(repository.Path), "profiles")
	}
	if len(result) > 0 {
		return result, nil
	}
	clean := filepath.Clean(active)
	marker := string(filepath.Separator) + "profiles" + string(filepath.Separator)
	index := strings.Index(clean, marker)
	if index < 0 {
		return nil, fmt.Errorf("%w: active profile %q is outside a repository profiles directory", ErrInvalidData, active)
	}
	repositoryRoot := clean[:index]
	result[filepath.Base(repositoryRoot)] = filepath.Join(repositoryRoot, "profiles")
	return result, nil
}

func withinAnyProfileRoot(path string, repositories map[string]string) bool {
	for _, root := range repositories {
		if withinRoot(path, root) {
			return true
		}
	}
	return false
}

func withinRoot(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func mergeProfileLayer(result *Profile, layer ProfileLayer) {
	for name, value := range layer.MakeDefaults {
		result.MakeDefaults[name] = value
	}
	result.System = applyProfileChanges(result.System, layer.System)
	result.PackageProvided = applyProfileChanges(result.PackageProvided, layer.PackageProvided)
	result.UseForce = applyProfileChanges(result.UseForce, layer.UseForce)
	result.UseMask = applyProfileChanges(result.UseMask, layer.UseMask)
	result.UseStableForce = applyProfileChanges(result.UseStableForce, layer.UseStableForce)
	result.UseStableMask = applyProfileChanges(result.UseStableMask, layer.UseStableMask)
	result.PackageUse = append(result.PackageUse, cloneRules(layer.PackageUse)...)
	result.PackageUseForce = append(result.PackageUseForce, cloneRules(layer.PackageUseForce)...)
	result.PackageUseMask = append(result.PackageUseMask, cloneRules(layer.PackageUseMask)...)
	result.PackageUseStableForce = append(result.PackageUseStableForce, cloneRules(layer.PackageUseStableForce)...)
	result.PackageUseStableMask = append(result.PackageUseStableMask, cloneRules(layer.PackageUseStableMask)...)
}

func applyProfileChanges(previous, changes []string) []string {
	result := append([]string(nil), previous...)
	for _, change := range changes {
		if name, remove := strings.CutPrefix(change, "-"); remove {
			filtered := result[:0]
			for _, existing := range result {
				if existing != name {
					filtered = append(filtered, existing)
				}
			}
			result = filtered
			continue
		}
		found := false
		for _, existing := range result {
			found = found || existing == change
		}
		if !found {
			result = append(result, change)
		}
	}
	return result
}

func cloneProfileLayer(layer ProfileLayer) ProfileLayer {
	layer.Parents = append([]string(nil), layer.Parents...)
	layer.MakeDefaults = cloneStringMap(layer.MakeDefaults)
	layer.System = append([]string(nil), layer.System...)
	layer.PackageProvided = append([]string(nil), layer.PackageProvided...)
	layer.UseForce = append([]string(nil), layer.UseForce...)
	layer.UseMask = append([]string(nil), layer.UseMask...)
	layer.UseStableForce = append([]string(nil), layer.UseStableForce...)
	layer.UseStableMask = append([]string(nil), layer.UseStableMask...)
	layer.PackageUse = cloneRules(layer.PackageUse)
	layer.PackageUseForce = cloneRules(layer.PackageUseForce)
	layer.PackageUseMask = cloneRules(layer.PackageUseMask)
	layer.PackageUseStableForce = cloneRules(layer.PackageUseStableForce)
	layer.PackageUseStableMask = cloneRules(layer.PackageUseStableMask)
	return layer
}

func cloneRules(rules []PackageFlagRule) []PackageFlagRule {
	result := make([]PackageFlagRule, len(rules))
	for index, rule := range rules {
		result[index] = rule
		result[index].Flags = append([]string(nil), rule.Flags...)
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}

func stripProfileQuotes(value string) string {
	for len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		value = value[1 : len(value)-1]
	}
	return value
}

// SortedRepositoryNames provides deterministic diagnostics for configured
// repository maps.
func SortedRepositoryNames(repositories []RepositoryPath) []string {
	names := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		names = append(names, repository.Name)
	}
	sort.Strings(names)
	return names
}
