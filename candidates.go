package gentooling

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// RepositoryCandidate is the evaluated metadata Portage records for one
// repository package version.
type RepositoryCandidate struct {
	ID           PackageID
	EAPI         string
	Keywords     []string
	DeclaredUse  []UseDeclaration
	Inherited    []string
	RequiredUse  string
	Dependencies DependencyMetadata
	MetadataPath string
}

type CandidateInventory struct {
	Candidates []RepositoryCandidate
	Issues     []Issue
}

type CandidateOptions struct {
	Integrity IntegrityMode
	Workers   int

	afterMetadataRead func(string)
}

type candidateTask struct {
	repository Repository
	category   string
	path       string
}

type candidateResult struct {
	candidate RepositoryCandidate
	issue     *Issue
}

// ReadRepositoryCandidates reads evaluated repository metadata from
// metadata/md5-cache. It never evaluates ebuild shell code.
func ReadRepositoryCandidates(ctx context.Context, repositories []Repository, options CandidateOptions) (CandidateInventory, error) {
	if options.Integrity != AllowPartial && options.Integrity != RequireComplete {
		return CandidateInventory{}, fmt.Errorf("%w: unknown candidate integrity mode %d", ErrInvalidData, options.Integrity)
	}
	if options.Workers < 0 {
		return CandidateInventory{}, fmt.Errorf("%w: candidate workers must not be negative", ErrInvalidData)
	}
	if err := ctx.Err(); err != nil {
		return CandidateInventory{}, err
	}
	var tasks []candidateTask
	var issues []Issue
	for _, repository := range repositories {
		if !validRepositoryName(repository.Name) || !filepath.IsAbs(repository.Location) {
			return CandidateInventory{}, fmt.Errorf("%w: candidate repository has invalid name or location: %q at %q", ErrInvalidData, repository.Name, repository.Location)
		}
		discovered, discoveredIssues, err := candidateTasks(ctx, repository)
		if err != nil {
			return CandidateInventory{}, err
		}
		tasks = append(tasks, discovered...)
		issues = append(issues, discoveredIssues...)
	}
	workers := options.Workers
	if workers == 0 {
		workers = runtime.GOMAXPROCS(0)
		if workers > 8 {
			workers = 8
		}
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}
	taskChannel := make(chan candidateTask)
	resultChannel := make(chan candidateResult, len(tasks))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for task := range taskChannel {
				if err := ctx.Err(); err != nil {
					return
				}
				candidate, issue := readRepositoryCandidate(task, options.afterMetadataRead)
				resultChannel <- candidateResult{candidate: candidate, issue: issue}
			}
		}()
	}
	go func() {
		for _, task := range tasks {
			select {
			case taskChannel <- task:
			case <-ctx.Done():
				close(taskChannel)
				group.Wait()
				close(resultChannel)
				return
			}
		}
		close(taskChannel)
		group.Wait()
		close(resultChannel)
	}()
	var result CandidateInventory
	for item := range resultChannel {
		if item.issue != nil {
			issues = append(issues, *item.issue)
		} else {
			result.Candidates = append(result.Candidates, item.candidate)
		}
	}
	if err := ctx.Err(); err != nil {
		return CandidateInventory{}, err
	}
	sort.Slice(result.Candidates, func(i, j int) bool {
		left, right := result.Candidates[i].ID, result.Candidates[j].ID
		if left.CP() != right.CP() {
			return left.CP() < right.CP()
		}
		leftVersion, _ := ParseVersion(left.Version)
		rightVersion, _ := ParseVersion(right.Version)
		if compared := leftVersion.Compare(rightVersion); compared != 0 {
			return compared < 0
		}
		return left.Repository < right.Repository
	})
	sort.Slice(issues, func(i, j int) bool { return issues[i].Path < issues[j].Path })
	result.Issues = issues
	if options.Integrity == RequireComplete && len(issues) != 0 {
		return result, &IntegrityError{Issues: append([]Issue(nil), issues...)}
	}
	return result, nil
}

func candidateTasks(ctx context.Context, repository Repository) ([]candidateTask, []Issue, error) {
	root := filepath.Join(repository.Location, "metadata", "md5-cache")
	info, err := os.Lstat(root)
	if err != nil {
		issue := Issue{Code: IssueUnreadableRecord, Path: root, Message: "repository metadata cache is unavailable", Cause: err}
		return nil, []Issue{issue}, nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		issue := Issue{Code: IssueInvalidMetadata, Path: root, Message: "repository metadata cache is not a directory", Cause: ErrInvalidData}
		return nil, []Issue{issue}, nil
	}
	categories, err := os.ReadDir(root)
	if err != nil {
		issue := Issue{Code: IssueUnreadableRecord, Path: root, Message: "repository metadata cache is unreadable", Cause: err}
		return nil, []Issue{issue}, nil
	}
	var tasks []candidateTask
	var issues []Issue
	for _, category := range categories {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		categoryPath := filepath.Join(root, category.Name())
		if category.Type()&os.ModeSymlink != 0 {
			issues = append(issues, Issue{Code: IssueInvalidMetadata, Path: categoryPath, Message: "repository category cache is a symlink", Cause: ErrInvalidData})
			continue
		}
		if !category.IsDir() {
			continue
		}
		entries, err := os.ReadDir(categoryPath)
		if err != nil {
			issue := Issue{Code: IssueUnreadableRecord, Path: categoryPath, Message: "repository category cache is unreadable", Cause: err}
			issues = append(issues, issue)
			continue
		}
		for _, entry := range entries {
			entryPath := filepath.Join(categoryPath, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				issues = append(issues, Issue{Code: IssueInvalidMetadata, Path: entryPath, Message: "candidate metadata is a symlink", Cause: ErrInvalidData})
				continue
			}
			if entry.Type().IsRegular() {
				tasks = append(tasks, candidateTask{repository: repository, category: category.Name(), path: entryPath})
			}
		}
	}
	return tasks, issues, nil
}

func readRepositoryCandidate(task candidateTask, afterRead func(string)) (RepositoryCandidate, *Issue) {
	id, err := ParsePackageID(task.category + "/" + filepath.Base(task.path))
	if err != nil {
		issue := Issue{Code: IssueMalformedIdentity, Path: task.path, Message: err.Error(), Cause: err}
		return RepositoryCandidate{}, &issue
	}
	id.Repository = task.repository.Name
	info, err := os.Lstat(task.path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = ErrInvalidData
		}
		issue := Issue{Code: IssueUnreadableRecord, Path: task.path, Package: &id, Message: "candidate metadata is not a regular file", Cause: err}
		return RepositoryCandidate{}, &issue
	}
	file, err := os.Open(task.path)
	if err != nil {
		issue := Issue{Code: IssueUnreadableRecord, Path: task.path, Package: &id, Message: "candidate metadata is unreadable", Cause: err}
		return RepositoryCandidate{}, &issue
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		name, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			values[name] = value
		}
	}
	if err := scanner.Err(); err != nil {
		issue := Issue{Code: IssueUnreadableRecord, Path: task.path, Package: &id, Message: "candidate metadata could not be read", Cause: err}
		return RepositoryCandidate{}, &issue
	}
	if afterRead != nil {
		afterRead(task.path)
	}
	after, err := os.Lstat(task.path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		if err == nil {
			err = ErrConcurrentMutation
		}
		issue := Issue{Code: IssueConcurrentMutation, Path: task.path, Package: &id, Message: "candidate metadata changed while it was read", Cause: err}
		return RepositoryCandidate{}, &issue
	}
	if values["EAPI"] == "" || values["SLOT"] == "" {
		issue := Issue{Code: IssueInvalidMetadata, Path: task.path, Package: &id, Message: "candidate metadata lacks EAPI or SLOT", Cause: ErrInvalidData}
		return RepositoryCandidate{}, &issue
	}
	id.Slot, id.Subslot, _ = strings.Cut(values["SLOT"], "/")
	inherited := strings.Fields(values["INHERITED"])
	if len(inherited) == 0 {
		fields := strings.Fields(values["_eclasses_"])
		if len(fields)%2 != 0 {
			issue := Issue{Code: IssueInvalidMetadata, Path: task.path, Package: &id, Message: "candidate metadata has malformed _eclasses_", Cause: ErrInvalidData}
			return RepositoryCandidate{}, &issue
		}
		for index := 0; index+1 < len(fields); index += 2 {
			inherited = append(inherited, fields[index])
		}
	}
	candidate := RepositoryCandidate{
		ID: id, EAPI: values["EAPI"], Keywords: strings.Fields(values["KEYWORDS"]),
		Inherited: inherited, RequiredUse: values["REQUIRED_USE"],
		Dependencies: DependencyMetadata{
			Depend: values["DEPEND"], RDepend: values["RDEPEND"], BDepend: values["BDEPEND"],
			IDepend: values["IDEPEND"], PDepend: values["PDEPEND"],
		},
		MetadataPath: task.path,
	}
	for _, declaration := range strings.Fields(values["IUSE"]) {
		candidate.DeclaredUse = append(candidate.DeclaredUse, parseUseDeclaration(declaration))
	}
	return candidate, nil
}

func parseUseDeclaration(value string) UseDeclaration {
	declaration := UseDeclaration{Name: value}
	if strings.HasPrefix(value, "+") {
		declaration.Name, declaration.Default = strings.TrimPrefix(value, "+"), UseDefaultEnabled
	} else if strings.HasPrefix(value, "-") {
		declaration.Name, declaration.Default = strings.TrimPrefix(value, "-"), UseDefaultDisabled
	}
	return declaration
}
