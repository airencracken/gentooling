package gentooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PreservedLibraryRecord is one Portage preserved-libraries owner record.
type PreservedLibraryRecord struct {
	Key         string
	Owner       PackageID
	Counter     string
	Paths       []string
	RootedPaths []string
}

// ReadPreservedLibraries validates and reads Portage's preserved library
// registry. A missing, empty, or whitespace-only registry is an empty state.
func ReadPreservedLibraries(ctx context.Context, root, registryPath string) ([]PreservedLibraryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" || registryPath == "" {
		return nil, fmt.Errorf("%w: preserved-library root and registry path are required", ErrInvalidData)
	}
	data, err := os.ReadFile(registryPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read preserved-library registry %q: %w", registryPath, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var raw map[string][]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: parse preserved-library registry %q: %v", ErrInvalidData, registryPath, err)
	}
	result := make([]PreservedLibraryRecord, 0, len(raw))
	for key, fields := range raw {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: preserved-library record %q has %d fields", ErrInvalidData, key, len(fields))
		}
		var owner, counter string
		var paths []string
		if json.Unmarshal(fields[0], &owner) != nil || json.Unmarshal(fields[1], &counter) != nil || json.Unmarshal(fields[2], &paths) != nil {
			return nil, fmt.Errorf("%w: malformed preserved-library record %q", ErrInvalidData, key)
		}
		id, err := ParsePackageID(owner)
		if err != nil {
			return nil, fmt.Errorf("%w: preserved-library owner %q", ErrInvalidData, owner)
		}
		record := PreservedLibraryRecord{Key: key, Owner: id, Counter: counter}
		seen := make(map[string]bool)
		for _, path := range paths {
			clean := filepath.Clean(path)
			if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
				return nil, fmt.Errorf("%w: unsafe preserved-library path %q", ErrInvalidData, path)
			}
			if seen[clean] {
				continue
			}
			seen[clean] = true
			record.Paths = append(record.Paths, filepath.ToSlash(clean))
			record.RootedPaths = append(record.RootedPaths, filepath.Join(filepath.Clean(root), strings.TrimPrefix(clean, string(filepath.Separator))))
		}
		sort.Strings(record.Paths)
		sort.Strings(record.RootedPaths)
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}
