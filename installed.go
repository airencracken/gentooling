package gentooling

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type UseDefault uint8

const (
	UseDefaultUnspecified UseDefault = iota
	UseDefaultEnabled
	UseDefaultDisabled
)

type UseDeclaration struct {
	Name    string
	Default UseDefault
}

type IssueCode string

const (
	IssueMalformedIdentity  IssueCode = "malformed_identity"
	IssueInterruptedRecord  IssueCode = "interrupted_record"
	IssueCorruptRecord      IssueCode = "corrupt_record"
	IssueUnreadableRecord   IssueCode = "unreadable_record"
	IssueInvalidMetadata    IssueCode = "invalid_metadata"
	IssueConcurrentMutation IssueCode = "concurrent_mutation"
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
	DeclaredUse  []UseDeclaration
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
	// Workers bounds concurrent record reads. Zero chooses a safe runtime-based
	// default. Negative values are invalid.
	Workers int

	beforeRecordRead      func(string)
	afterRequiredSnapshot func(string)
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

type recordTask struct {
	index int
	dir   string
	id    PackageID
}

type fileSnapshot struct {
	path string
	info fs.FileInfo
}

type recordResult struct {
	index     int
	pkg       InstalledPackage
	issues    []Issue
	snapshots []fileSnapshot
	ok        bool
}

// ReadInstalled reads Portage's installed-package database. AllowPartial
// returns stable records alongside typed issues. RequireComplete returns the
// same diagnostic result plus ErrIncompleteEvidence when any evidence is
// incomplete. The scan detects observed mutations without claiming an atomic
// snapshot in the absence of a package-manager transaction lock.
func ReadInstalled(ctx context.Context, paths SystemPaths, options InstalledOptions) (InstalledInventory, error) {
	if err := validateInstalledOptions(options); err != nil {
		return InstalledInventory{}, err
	}
	if err := ctx.Err(); err != nil {
		return InstalledInventory{}, err
	}
	root := paths.VDB
	if root == "" {
		return InstalledInventory{}, fmt.Errorf("%w: VDB path is empty", ErrInvalidData)
	}
	rootStart, err := os.Lstat(root)
	if err != nil {
		return InstalledInventory{}, fmt.Errorf("read installed package root %q: %w", root, err)
	}
	if !rootStart.IsDir() {
		return InstalledInventory{}, fmt.Errorf("%w: installed package root %q is not a directory", ErrInvalidData, root)
	}
	categories, err := os.ReadDir(root)
	if err != nil {
		return InstalledInventory{}, fmt.Errorf("read installed package root %q: %w", root, err)
	}

	var tasks []recordTask
	var categorySnapshots []fileSnapshot
	result := InstalledInventory{}
	for _, category := range categories {
		if err := ctx.Err(); err != nil {
			return InstalledInventory{}, err
		}
		if !category.IsDir() {
			continue
		}
		categoryPath := filepath.Join(root, category.Name())
		categoryInfo, statErr := os.Lstat(categoryPath)
		if statErr != nil {
			return InstalledInventory{}, fmt.Errorf("inspect installed package category %q: %w", categoryPath, statErr)
		}
		categorySnapshots = append(categorySnapshots, fileSnapshot{path: categoryPath, info: categoryInfo})
		entries, readErr := os.ReadDir(categoryPath)
		if readErr != nil {
			return InstalledInventory{}, fmt.Errorf("read installed package category %q: %w", categoryPath, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(categoryPath, entry.Name())
			id, parseErr := ParsePackageID(category.Name() + "/" + entry.Name())
			if parseErr != nil {
				result.Issues = append(result.Issues, newIssue(IssueMalformedIdentity, dir, nil, "invalid installed package identity", errors.Join(ErrCorruptRecord, parseErr)))
				continue
			}
			tasks = append(tasks, recordTask{index: len(tasks), dir: dir, id: id})
		}
	}

	records, err := readRecordsConcurrently(ctx, tasks, options)
	if err != nil {
		return InstalledInventory{}, err
	}
	if err := validateRecordsConcurrently(ctx, records, installedWorkerCount(options.Workers, len(records))); err != nil {
		return InstalledInventory{}, err
	}
	for _, record := range records {
		result.Issues = append(result.Issues, record.issues...)
		if record.ok {
			result.Packages = append(result.Packages, record.pkg)
		}
	}
	if mutation := validateSnapshot(fileSnapshot{path: root, info: rootStart}, nil); mutation != nil {
		result.Issues = append(result.Issues, *mutation)
	}
	for _, snapshot := range categorySnapshots {
		if mutation := validateSnapshot(snapshot, nil); mutation != nil {
			result.Issues = append(result.Issues, *mutation)
		}
	}

	sortInventory(&result)
	if options.Integrity == RequireComplete && len(result.Issues) > 0 {
		return result, &IntegrityError{Issues: append([]Issue(nil), result.Issues...)}
	}
	return result, nil
}

func validateInstalledOptions(options InstalledOptions) error {
	if options.Integrity != AllowPartial && options.Integrity != RequireComplete {
		return fmt.Errorf("%w: unknown installed integrity mode %d", ErrInvalidData, options.Integrity)
	}
	if options.Workers < 0 {
		return fmt.Errorf("%w: installed workers must not be negative", ErrInvalidData)
	}
	return nil
}

func readRecordsConcurrently(ctx context.Context, tasks []recordTask, options InstalledOptions) ([]recordResult, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	workers := installedWorkerCount(options.Workers, len(tasks))
	work := make(chan recordTask)
	completed := make(chan recordResult, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for task := range work {
				if options.beforeRecordRead != nil {
					options.beforeRecordRead(task.dir)
				}
				pkg, issues, snapshots, ok := readInstalledRecord(
					task.dir, task.id, options.IncludeContents, options.afterRequiredSnapshot,
				)
				select {
				case completed <- recordResult{index: task.index, pkg: pkg, issues: issues, snapshots: snapshots, ok: ok}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, task := range tasks {
			select {
			case work <- task:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(completed)
	}()

	records := make([]recordResult, 0, len(tasks))
	for record := range completed {
		records = append(records, record)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].index < records[j].index })
	return records, nil
}

func installedWorkerCount(requested, tasks int) int {
	if tasks <= 0 {
		return 0
	}
	workers := requested
	if workers == 0 {
		workers = 1
		if tasks >= 32 {
			workers = runtime.GOMAXPROCS(0)
			if workers > 32 {
				workers = 32
			}
		}
	}
	if workers > tasks {
		workers = tasks
	}
	return workers
}

func validateRecordsConcurrently(ctx context.Context, records []recordResult, workers int) error {
	if len(records) == 0 {
		return nil
	}
	work := make(chan int)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range work {
				record := &records[index]
				if !record.ok {
					continue
				}
				if mutation := validateSnapshots(record.snapshots, &record.pkg.ID); mutation != nil {
					record.issues = append(record.issues, *mutation)
					record.ok = false
				}
			}
		}()
	}
	for index := range records {
		select {
		case work <- index:
		case <-ctx.Done():
			close(work)
			group.Wait()
			return ctx.Err()
		}
	}
	close(work)
	group.Wait()
	return ctx.Err()
}

func readInstalledRecord(dir string, id PackageID, includeContents bool, afterRequiredSnapshot func(string)) (InstalledPackage, []Issue, []fileSnapshot, bool) {
	required := []string{"CONTENTS", "EAPI", "SLOT", "repository"}
	var snapshots []fileSnapshot
	for _, name := range required {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				issue := newIssue(IssueInterruptedRecord, path, &id, "minimum committed metadata is absent", errors.Join(ErrInterruptedRecord, err))
				return InstalledPackage{}, []Issue{issue}, nil, false
			}
			issue := newIssue(IssueUnreadableRecord, path, &id, "cannot inspect required metadata", errors.Join(ErrUnreadableRecord, err))
			return InstalledPackage{}, []Issue{issue}, nil, false
		}
		if !info.Mode().IsRegular() {
			issue := newIssue(IssueCorruptRecord, path, &id, "required metadata is not a regular file", ErrCorruptRecord)
			return InstalledPackage{}, []Issue{issue}, nil, false
		}
		snapshots = append(snapshots, fileSnapshot{path: path, info: info})
	}
	if afterRequiredSnapshot != nil {
		afterRequiredSnapshot(dir)
	}

	values := make(map[string]string)
	for _, name := range []string{"EAPI", "SLOT", "repository"} {
		value, snapshot, issue := readRegularFile(filepath.Join(dir, name), id, true)
		if issue != nil {
			return InstalledPackage{}, []Issue{*issue}, nil, false
		}
		values[name] = value
		snapshots = append(snapshots, snapshot)
	}
	for _, name := range []string{"EAPI", "SLOT", "repository"} {
		if values[name] == "" {
			issue := newIssue(IssueCorruptRecord, filepath.Join(dir, name), &id, "required metadata is empty", ErrCorruptRecord)
			return InstalledPackage{}, []Issue{issue}, nil, false
		}
	}
	if includeContents {
		value, snapshot, issue := readRegularFile(filepath.Join(dir, "CONTENTS"), id, true)
		if issue != nil {
			return InstalledPackage{}, []Issue{*issue}, nil, false
		}
		values["CONTENTS"] = value
		snapshots = append(snapshots, snapshot)
	}

	slot, subslot := splitSlot(values["SLOT"])
	id.Slot, id.Subslot, id.Repository = slot, subslot, values["repository"]
	var issues []Issue
	optional := func(name string) string {
		value, snapshot, issue := readRegularFile(filepath.Join(dir, name), id, false)
		if snapshot.info != nil {
			snapshots = append(snapshots, snapshot)
		}
		if issue != nil {
			issues = append(issues, *issue)
		}
		return value
	}
	enabled := strings.Fields(optional("USE"))
	declared, declarationIssues := parseUseDeclarations(filepath.Join(dir, "IUSE"), id, optional("IUSE"))
	issues = append(issues, declarationIssues...)
	pkg := InstalledPackage{
		ID: id, EAPI: values["EAPI"],
		EnabledUse:  enabled,
		DeclaredUse: declared,
		Dependencies: DependencyMetadata{
			Depend: optional("DEPEND"), RDepend: optional("RDEPEND"), BDepend: optional("BDEPEND"),
			IDepend: optional("IDEPEND"), PDepend: optional("PDEPEND"),
		},
		Build:    BuildMetadata{ID: optional("BUILD_ID"), PhaseEnvABI: optional("ARISE_PHASE_ENV_ABI")},
		Contents: values["CONTENTS"],
	}
	pkg.Build.Time, issues = parseOptionalInt(dir, id, "BUILD_TIME", optional("BUILD_TIME"), issues)
	pkg.Build.Counter, issues = parseOptionalInt(dir, id, "COUNTER", optional("COUNTER"), issues)
	return pkg, issues, snapshots, true
}

func readRegularFile(path string, id PackageID, required bool) (string, fileSnapshot, *Issue) {
	info, err := os.Lstat(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return "", fileSnapshot{}, nil
		}
		code, category, message := IssueUnreadableRecord, ErrUnreadableRecord, "cannot inspect metadata"
		if required && errors.Is(err, fs.ErrNotExist) {
			code, category, message = IssueInterruptedRecord, ErrInterruptedRecord, "minimum committed metadata is absent"
		}
		issue := newIssue(code, path, &id, message, errors.Join(category, err))
		return "", fileSnapshot{}, &issue
	}
	if !info.Mode().IsRegular() {
		issue := newIssue(IssueCorruptRecord, path, &id, "metadata is not a regular file", ErrCorruptRecord)
		return "", fileSnapshot{}, &issue
	}
	data, err := os.ReadFile(path)
	if err != nil {
		issue := newIssue(IssueUnreadableRecord, path, &id, "cannot read metadata", errors.Join(ErrUnreadableRecord, err))
		return "", fileSnapshot{}, &issue
	}
	snapshot := fileSnapshot{path: path, info: info}
	if mutation := validateSnapshot(snapshot, &id); mutation != nil {
		return "", fileSnapshot{}, mutation
	}
	return strings.TrimSpace(string(data)), snapshot, nil
}

func parseUseDeclarations(path string, id PackageID, value string) ([]UseDeclaration, []Issue) {
	var declarations []UseDeclaration
	var issues []Issue
	for _, raw := range strings.Fields(value) {
		declaration := UseDeclaration{Name: raw}
		switch raw[0] {
		case '+':
			declaration.Name = raw[1:]
			declaration.Default = UseDefaultEnabled
		case '-':
			declaration.Name = raw[1:]
			declaration.Default = UseDefaultDisabled
		}
		if declaration.Name == "" {
			issues = append(issues, newIssue(IssueInvalidMetadata, path, &id, "empty IUSE declaration", errors.Join(ErrCorruptRecord, ErrInvalidData)))
			continue
		}
		declarations = append(declarations, declaration)
	}
	return declarations, issues
}

func validateSnapshots(snapshots []fileSnapshot, id *PackageID) *Issue {
	for _, snapshot := range snapshots {
		if mutation := validateSnapshot(snapshot, id); mutation != nil {
			return mutation
		}
	}
	return nil
}

func validateSnapshot(snapshot fileSnapshot, id *PackageID) *Issue {
	current, err := os.Lstat(snapshot.path)
	if err != nil || !os.SameFile(snapshot.info, current) ||
		snapshot.info.Mode() != current.Mode() ||
		snapshot.info.Size() != current.Size() ||
		!snapshot.info.ModTime().Equal(current.ModTime()) {
		cause := ErrConcurrentMutation
		if err != nil {
			cause = errors.Join(ErrConcurrentMutation, err)
		}
		issue := newIssue(IssueConcurrentMutation, snapshot.path, id, "metadata changed during installed-package scan", cause)
		return &issue
	}
	return nil
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

func sortInventory(result *InstalledInventory) {
	sort.Slice(result.Packages, func(i, j int) bool {
		return result.Packages[i].ID.CPV() < result.Packages[j].ID.CPV()
	})
	sort.SliceStable(result.Issues, func(i, j int) bool {
		if result.Issues[i].Path == result.Issues[j].Path {
			return result.Issues[i].Code < result.Issues[j].Code
		}
		return result.Issues[i].Path < result.Issues[j].Path
	})
}

func newIssue(code IssueCode, path string, id *PackageID, message string, cause error) Issue {
	var copied *PackageID
	if id != nil {
		value := *id
		copied = &value
	}
	return Issue{Code: code, Path: path, Package: copied, Message: message, Cause: cause}
}
