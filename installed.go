package gentooling

import (
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

var (
	ErrIncompleteEvidence = errors.New("gentooling: incomplete evidence")
	ErrInterruptedRecord  = errors.New("gentooling: interrupted package record")
	ErrCorruptRecord      = errors.New("gentooling: corrupt package record")
	ErrUnreadableRecord   = errors.New("gentooling: unreadable package record")
	ErrConcurrentMutation = errors.New("gentooling: concurrent mutation")
)

type IntegrityMode uint8

const (
	AllowPartial IntegrityMode = iota
	RequireComplete
)

type IssueCode string

const (
	IssueMalformedIdentity IssueCode = "malformed_identity"
	IssueInterruptedRecord IssueCode = "interrupted_record"
	IssueCorruptRecord     IssueCode = "corrupt_record"
	IssueUnreadableRecord  IssueCode = "unreadable_record"
	IssueInvalidMetadata   IssueCode = "invalid_metadata"
)

type Issue struct {
	Code    IssueCode
	Path    string
	Package *PackageID
	Message string
	Cause   error
}

func (i Issue) Error() string {
	if i.Path == "" {
		return i.Message
	}
	return fmt.Sprintf("%s: %s", i.Path, i.Message)
}

func (i Issue) Unwrap() error {
	return i.Cause
}

type DependencyMetadata struct {
	Depend  string
	RDepend string
	BDepend string
	IDepend string
	PDepend string
}

type BuildMetadata struct {
	Time        int64
	ID          string
	Counter     int64
	PhaseEnvABI string
}

type InstalledPackage struct {
	ID           PackageID
	EAPI         string
	EnabledUse   []string
	DeclaredUse  []string
	Dependencies DependencyMetadata
	Build        BuildMetadata
	Contents     string
}

type InstalledInventory struct {
	Packages []InstalledPackage
	Issues   []Issue
}

type InstalledOptions struct {
	Integrity       IntegrityMode
	IncludeContents bool
}

type IntegrityError struct {
	Issues []Issue
}

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("%v: %d issue(s)", ErrIncompleteEvidence, len(e.Issues))
}

func (e *IntegrityError) Unwrap() error {
	return ErrIncompleteEvidence
}

// ReadInstalled reads Portage's installed-package database. AllowPartial
// returns trustworthy records alongside typed issues. RequireComplete returns
// the same diagnostic result plus ErrIncompleteEvidence when any record could
// not be classified confidently.
func ReadInstalled(ctx context.Context, paths SystemPaths, options InstalledOptions) (InstalledInventory, error) {
	if err := ctx.Err(); err != nil {
		return InstalledInventory{}, err
	}
	root := paths.VDB
	if root == "" {
		return InstalledInventory{}, fmt.Errorf("%w: VDB path is empty", ErrInvalidData)
	}
	categories, err := os.ReadDir(root)
	if err != nil {
		return InstalledInventory{}, fmt.Errorf("read installed package root %q: %w", root, err)
	}
	result := InstalledInventory{}
	for _, category := range categories {
		if err := ctx.Err(); err != nil {
			return InstalledInventory{}, err
		}
		if !category.IsDir() {
			continue
		}
		categoryPath := filepath.Join(root, category.Name())
		entries, readErr := os.ReadDir(categoryPath)
		if readErr != nil {
			return InstalledInventory{}, fmt.Errorf("read installed package category %q: %w", categoryPath, readErr)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return InstalledInventory{}, err
			}
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(categoryPath, entry.Name())
			id, parseErr := ParsePackageID(category.Name() + "/" + entry.Name())
			if parseErr != nil {
				result.Issues = append(result.Issues, newIssue(IssueMalformedIdentity, dir, nil, "invalid installed package identity", errors.Join(ErrCorruptRecord, parseErr)))
				continue
			}
			pkg, issues, ok := readInstalledRecord(dir, id, options.IncludeContents)
			result.Issues = append(result.Issues, issues...)
			if ok {
				result.Packages = append(result.Packages, pkg)
			}
		}
	}
	sort.Slice(result.Packages, func(i, j int) bool {
		return result.Packages[i].ID.CPV() < result.Packages[j].ID.CPV()
	})
	sort.SliceStable(result.Issues, func(i, j int) bool {
		if result.Issues[i].Path == result.Issues[j].Path {
			return result.Issues[i].Code < result.Issues[j].Code
		}
		return result.Issues[i].Path < result.Issues[j].Path
	})
	if options.Integrity == RequireComplete && len(result.Issues) > 0 {
		return result, &IntegrityError{Issues: append([]Issue(nil), result.Issues...)}
	}
	return result, nil
}

func readInstalledRecord(dir string, id PackageID, includeContents bool) (InstalledPackage, []Issue, bool) {
	required := []string{"CONTENTS", "EAPI", "SLOT", "repository"}
	for _, name := range required {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				issue := newIssue(IssueInterruptedRecord, filepath.Join(dir, name), &id, "minimum committed metadata is absent", errors.Join(ErrInterruptedRecord, err))
				return InstalledPackage{}, []Issue{issue}, false
			}
			issue := newIssue(IssueUnreadableRecord, filepath.Join(dir, name), &id, "cannot inspect required metadata", errors.Join(ErrUnreadableRecord, err))
			return InstalledPackage{}, []Issue{issue}, false
		}
		if !info.Mode().IsRegular() {
			issue := newIssue(IssueCorruptRecord, filepath.Join(dir, name), &id, "required metadata is not a regular file", ErrCorruptRecord)
			return InstalledPackage{}, []Issue{issue}, false
		}
	}
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	values := make(map[string]string)
	for _, name := range required {
		value, err := read(name)
		if err != nil {
			issue := newIssue(IssueUnreadableRecord, filepath.Join(dir, name), &id, "cannot read required metadata", errors.Join(ErrUnreadableRecord, err))
			return InstalledPackage{}, []Issue{issue}, false
		}
		values[name] = value
	}
	for _, name := range []string{"EAPI", "SLOT", "repository"} {
		if values[name] == "" {
			issue := newIssue(IssueCorruptRecord, filepath.Join(dir, name), &id, "required metadata is empty", ErrCorruptRecord)
			return InstalledPackage{}, []Issue{issue}, false
		}
	}
	slot, subslot := splitSlot(values["SLOT"])
	id.Slot, id.Subslot, id.Repository = slot, subslot, values["repository"]
	var issues []Issue
	optional := func(name string) string {
		path := filepath.Join(dir, name)
		value, err := read(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			issues = append(issues, newIssue(
				IssueUnreadableRecord, path, &id, "cannot read optional metadata",
				errors.Join(ErrUnreadableRecord, err),
			))
		}
		return value
	}
	pkg := InstalledPackage{
		ID: id, EAPI: values["EAPI"],
		EnabledUse:  strings.Fields(optional("USE")),
		DeclaredUse: strings.Fields(optional("IUSE")),
		Dependencies: DependencyMetadata{
			Depend: optional("DEPEND"), RDepend: optional("RDEPEND"), BDepend: optional("BDEPEND"),
			IDepend: optional("IDEPEND"), PDepend: optional("PDEPEND"),
		},
		Build: BuildMetadata{ID: optional("BUILD_ID"), PhaseEnvABI: optional("ARISE_PHASE_ENV_ABI")},
	}
	pkg.Build.Time, issues = parseOptionalInt(dir, id, "BUILD_TIME", optional("BUILD_TIME"), issues)
	pkg.Build.Counter, issues = parseOptionalInt(dir, id, "COUNTER", optional("COUNTER"), issues)
	if includeContents {
		pkg.Contents = values["CONTENTS"]
	}
	return pkg, issues, true
}

func parseOptionalInt(dir string, id PackageID, name, value string, issues []Issue) (int64, []Issue) {
	if value == "" {
		return 0, issues
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		return number, issues
	}
	issue := newIssue(IssueInvalidMetadata, filepath.Join(dir, name), &id, "integer metadata is invalid", errors.Join(ErrCorruptRecord, ErrInvalidData, err))
	return 0, append(issues, issue)
}

func splitSlot(value string) (string, string) {
	if slot, subslot, ok := strings.Cut(value, "/"); ok {
		return slot, subslot
	}
	return value, ""
}

func newIssue(code IssueCode, path string, id *PackageID, message string, cause error) Issue {
	var copied *PackageID
	if id != nil {
		value := *id
		copied = &value
	}
	return Issue{Code: code, Path: path, Package: copied, Message: message, Cause: cause}
}
