package gentooling

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// SelectionKind distinguishes package atoms from named package sets.
type SelectionKind uint8

const (
	PackageSelection SelectionKind = iota
	SetSelection
)

// Selection is one ordered world or system entry with source provenance.
type Selection struct {
	Value  string
	Kind   SelectionKind
	Atom   *Atom
	Source PolicySource
}

// Selections contains explicit user world entries and effective profile
// system entries. Both slices are deterministic and independently owned.
type Selections struct {
	World  []Selection
	System []Selection
}

// ReadSelections reads the world file and active profile system selection.
func ReadSelections(ctx context.Context, paths SystemPaths) (Selections, error) {
	if err := ctx.Err(); err != nil {
		return Selections{}, err
	}
	profile, err := ReadProfile(ctx, paths)
	if err != nil {
		return Selections{}, err
	}
	return readSelectionsWithProfile(ctx, paths.World, &profile)
}

func readSelectionsWithProfile(ctx context.Context, worldPath string, profile *Profile) (Selections, error) {
	world, err := readWorldSelections(ctx, worldPath)
	if err != nil {
		return Selections{}, err
	}
	system, err := selectionsFromProfile(profile)
	if err != nil {
		return Selections{}, err
	}
	sortSelections(world)
	return Selections{World: world, System: system}, nil
}

func selectionsFromProfile(profile *Profile) ([]Selection, error) {
	system := make([]Selection, 0)
	if profile != nil {
		for _, value := range profile.System {
			parsed, parseErr := ParseAtom(value)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: system selection %q: %v", ErrInvalidData, value, parseErr)
			}
			source := systemSelectionSource(*profile, value)
			system = append(system, Selection{Value: value, Kind: PackageSelection, Atom: &parsed, Source: source})
		}
	}
	sortSelections(system)
	return system, nil
}

func readWorldSelections(ctx context.Context, path string) ([]Selection, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: world path is empty", ErrInvalidData)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect world file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: world file %q is not a regular file", ErrInvalidData, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read world file %q: %w", path, err)
	}
	var result []Selection
	seen := make(map[string]bool)
	for index, raw := range strings.Split(string(data), "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		if strings.ContainsAny(value, " \t") || seen[value] {
			if seen[value] {
				continue
			}
			return nil, fmt.Errorf("%w: malformed world selection at %s:%d", ErrInvalidData, path, index+1)
		}
		selection := Selection{Value: value, Source: PolicySource{Path: path, Line: index + 1}}
		if strings.HasPrefix(value, "@") {
			if !validSetName(strings.TrimPrefix(value, "@")) {
				return nil, fmt.Errorf("%w: invalid set selection %q at %s:%d", ErrInvalidData, value, path, index+1)
			}
			selection.Kind = SetSelection
		} else {
			parsed, parseErr := ParseAtom(value)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid world selection %q at %s:%d: %v", ErrInvalidData, value, path, index+1, parseErr)
			}
			selection.Atom = &parsed
		}
		seen[value] = true
		result = append(result, selection)
	}
	return result, nil
}

func validSetName(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || character == '_' || character == '+' ||
			character == '.' || character == '-') {
			return false
		}
	}
	return true
}

func systemSelectionSource(profile Profile, value string) PolicySource {
	for layerIndex := len(profile.Layers) - 1; layerIndex >= 0; layerIndex-- {
		layer := profile.Layers[layerIndex]
		for _, candidate := range layer.System {
			if candidate == value {
				path := layer.Path + "/packages"
				return PolicySource{Path: path, Line: systemSelectionLine(path, value)}
			}
		}
	}
	return PolicySource{Path: profile.ActivePath + "/packages"}
}

func systemSelectionLine(path, value string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lineNumber := 0
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if strings.TrimPrefix(line, "*") == value && !strings.HasPrefix(line, "-") {
			lineNumber = index + 1
		}
	}
	return lineNumber
}

func sortSelections(input []Selection) {
	sort.SliceStable(input, func(left, right int) bool {
		return input[left].Value < input[right].Value
	})
}
