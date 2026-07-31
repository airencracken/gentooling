package gentooling

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var ErrRepositoryCycle = errors.New("gentooling: repository master cycle")

// Repository is one effective repos.conf section with root-aware paths.
type Repository struct {
	Name       string
	Location   string
	SyncType   string
	SyncURI    string
	CloneDepth *int
	SyncDepth  *int
	Priority   int
	AutoSync   bool
	Masters    []string
	Main       bool
	Source     PolicySource
}

type repositorySection struct {
	name       string
	values     map[string]string
	valueLines map[string]int
	source     string
}

// ReadRepositories discovers repositories from a root-aware repos.conf file or
// directory and returns them in deterministic master-before-child order.
func ReadRepositories(ctx context.Context, paths SystemPaths) ([]Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if paths.ReposConf == "" {
		return nil, fmt.Errorf("%w: repos.conf path is empty", ErrInvalidData)
	}
	files, err := repositoryConfigFiles(paths.ReposConf)
	if err != nil {
		return nil, err
	}
	var sections []repositorySection
	for _, file := range files {
		parsed, parseErr := readRepositorySections(ctx, file)
		if parseErr != nil {
			return nil, parseErr
		}
		sections = append(sections, parsed...)
	}
	defaults := make(map[string]string)
	mainRepository := ""
	byName := make(map[string]repositorySection)
	var names []string
	for _, section := range sections {
		if section.name == "DEFAULT" {
			for key, value := range section.values {
				defaults[key] = value
			}
			if value := section.values["main-repo"]; value != "" {
				mainRepository = value
			}
			continue
		}
		if section.name == "" || !validRepositoryName(section.name) {
			return nil, fmt.Errorf("%w: invalid repository section %q at %s", ErrInvalidData, section.name, section.source)
		}
		if _, exists := byName[section.name]; !exists {
			names = append(names, section.name)
		}
		byName[section.name] = section
	}
	repositories := make(map[string]Repository, len(byName))
	for _, name := range names {
		section := byName[name]
		values := make(map[string]string, len(defaults)+len(section.values))
		for key, value := range defaults {
			values[key] = value
		}
		for key, value := range section.values {
			values[key] = value
		}
		location, resolveErr := resolveRepositoryLocation(paths.Root, values["location"])
		if resolveErr != nil {
			return nil, fmt.Errorf("repository %s: %w", name, resolveErr)
		}
		repository := Repository{
			Name: name, Location: location, SyncType: values["sync-type"],
			SyncURI: values["sync-uri"], AutoSync: values["auto-sync"] != "no",
			Main:   name == mainRepository,
			Source: PolicySource{Path: section.source, Line: section.valueLines["location"]},
		}
		if value := values["priority"]; value != "" {
			priority, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: repository %s priority %q", ErrInvalidData, name, value)
			}
			repository.Priority = priority
		}
		if repository.CloneDepth, err = repositoryDepth("clone-depth", values["clone-depth"]); err != nil {
			return nil, fmt.Errorf("repository %s: %w", name, err)
		}
		if repository.SyncDepth, err = repositoryDepth("sync-depth", values["sync-depth"]); err != nil {
			return nil, fmt.Errorf("repository %s: %w", name, err)
		}
		repository.Masters, err = repositoryMasters(location)
		if err != nil {
			return nil, err
		}
		repositories[name] = repository
	}
	return orderRepositories(names, repositories)
}

func repositoryConfigFiles(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect repos.conf %q: %w", path, err)
	}
	if info.Mode().IsRegular() {
		return []string{path}, nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: repos.conf %q is not a regular file or directory", ErrInvalidData, path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: repos.conf entry %q is a symlink", ErrInvalidData, entryPath)
		}
		if entry.Type().IsRegular() && !strings.HasPrefix(entry.Name(), ".") && !strings.HasSuffix(entry.Name(), "~") {
			files = append(files, entryPath)
		}
	}
	sort.Strings(files)
	return files, nil
}

func readRepositorySections(ctx context.Context, path string) ([]repositorySection, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result []repositorySection
	var current *repositorySection
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current != nil {
				result = append(result, *current)
			}
			current = &repositorySection{
				name: strings.TrimSpace(line[1 : len(line)-1]), values: make(map[string]string),
				valueLines: make(map[string]int), source: path,
			}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%w: repos.conf assignment outside section at %s:%d", ErrInvalidData, path, lineNumber)
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%w: malformed repos.conf line at %s:%d", ErrInvalidData, path, lineNumber)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		current.values[key], current.valueLines[key] = value, lineNumber
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		result = append(result, *current)
	}
	return result, nil
}

func resolveRepositoryLocation(root, location string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("%w: repository location is empty", ErrInvalidData)
	}
	root = filepath.Clean(root)
	if root == "." || root == "" {
		return "", fmt.Errorf("%w: system root is empty", ErrInvalidData)
	}
	location = filepath.Clean(location)
	if !filepath.IsAbs(location) {
		return "", fmt.Errorf("%w: repository location %q is not absolute", ErrInvalidData, location)
	}
	if root == string(filepath.Separator) {
		return location, nil
	}
	if withinRoot(location, root) {
		return location, nil
	}
	resolved := filepath.Join(root, strings.TrimPrefix(location, string(filepath.Separator)))
	if !withinRoot(resolved, root) {
		return "", fmt.Errorf("%w: repository location %q escapes root %q", ErrInvalidData, location, root)
	}
	return resolved, nil
}

func repositoryDepth(name, value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	depth, err := strconv.Atoi(value)
	if err != nil || depth < 0 {
		return nil, fmt.Errorf("%w: %s must be a non-negative integer, got %q", ErrInvalidData, name, value)
	}
	return &depth, nil
}

func repositoryMasters(location string) ([]string, error) {
	path := filepath.Join(location, "metadata", "layout.conf")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository layout %q: %w", path, err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(raw, "=")
		if found && strings.TrimSpace(key) == "masters" {
			return strings.Fields(strings.TrimSpace(value)), nil
		}
	}
	return nil, nil
}

func orderRepositories(names []string, repositories map[string]Repository) ([]Repository, error) {
	var result []Repository
	state := make(map[string]uint8)
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("%w: %s", ErrRepositoryCycle, name)
		case 2:
			return nil
		}
		repository, exists := repositories[name]
		if !exists {
			return fmt.Errorf("%w: repository master %q is not configured", ErrInvalidData, name)
		}
		state[name] = 1
		for _, master := range repository.Masters {
			if err := visit(master); err != nil {
				return err
			}
		}
		state[name] = 2
		result = append(result, repository)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validRepositoryName(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func cloneRepositories(input []Repository) []Repository {
	result := make([]Repository, len(input))
	for index, repository := range input {
		result[index] = repository
		result[index].Masters = append([]string(nil), repository.Masters...)
		if repository.CloneDepth != nil {
			value := *repository.CloneDepth
			result[index].CloneDepth = &value
		}
		if repository.SyncDepth != nil {
			value := *repository.SyncDepth
			result[index].SyncDepth = &value
		}
	}
	return result
}
