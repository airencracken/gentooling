package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func writeCandidate(t *testing.T, root, category, packageVersion, metadata string) string {
	t.Helper()
	path := filepath.Join(root, "metadata", "md5-cache", category, packageVersion)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadRepositoryCandidatesReturnsEvaluatedMetadataDeterministically(t *testing.T) {
	root := t.TempDir()
	writeCandidate(t, root, "sys-kernel", "example-10.0", "EAPI=8\nSLOT=10/10.0\nKEYWORDS=amd64 ~arm64\nIUSE=+modules -debug test\nDEPEND=dev-libs/a\n")
	writeCandidate(t, root, "sys-kernel", "example-2.0", "EAPI=8\nSLOT=2\nKEYWORDS=amd64\nIUSE=modules\n")
	inventory, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{
		Integrity: RequireComplete,
		Workers:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Candidates) != 2 || inventory.Candidates[0].ID.Version != "2.0" || inventory.Candidates[1].ID.Version != "10.0" {
		t.Fatalf("candidate ordering = %+v", inventory.Candidates)
	}
	candidate := inventory.Candidates[1]
	if candidate.ID.Repository != "test" || candidate.ID.Slot != "10" || candidate.ID.Subslot != "10.0" ||
		!reflect.DeepEqual(candidate.Keywords, []string{"amd64", "~arm64"}) ||
		!reflect.DeepEqual(candidate.DeclaredUse, []UseDeclaration{
			{Name: "modules", Default: UseDefaultEnabled},
			{Name: "debug", Default: UseDefaultDisabled},
			{Name: "test", Default: UseDefaultUnspecified},
		}) || candidate.Dependencies.Depend != "dev-libs/a" {
		t.Fatalf("candidate metadata = %+v", candidate)
	}
}

func TestReadRepositoryCandidatesReportsPartialAndStrictEvidence(t *testing.T) {
	root := t.TempDir()
	writeCandidate(t, root, "app-misc", "valid-1", "EAPI=8\nSLOT=0\n")
	bad := writeCandidate(t, root, "app-misc", "broken", "EAPI=8\nSLOT=0\n")
	partial, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{
		Integrity: AllowPartial,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Candidates) != 1 || len(partial.Issues) != 1 || partial.Issues[0].Path != bad ||
		partial.Issues[0].Code != IssueMalformedIdentity {
		t.Fatalf("partial inventory = %+v", partial)
	}
	strict, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{
		Integrity: RequireComplete,
	})
	if !errors.Is(err, ErrIncompleteEvidence) || len(strict.Candidates) != 1 || len(strict.Issues) != 1 {
		t.Fatalf("strict inventory = %+v, %v", strict, err)
	}
}

func TestReadRepositoryCandidatesValidatesOptionsAndMetadata(t *testing.T) {
	for name, options := range map[string]CandidateOptions{
		"integrity": {Integrity: IntegrityMode(99)},
		"workers":   {Workers: -1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadRepositoryCandidates(context.Background(), nil, options)
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	_, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "../bad", Location: "relative"}}, CandidateOptions{})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("repository error = %v", err)
	}
	root := t.TempDir()
	path := writeCandidate(t, root, "app-misc", "example-1", "EAPI=8\n")
	inventory, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{})
	if err != nil || len(inventory.Issues) != 1 || inventory.Issues[0].Path != path ||
		inventory.Issues[0].Code != IssueInvalidMetadata {
		t.Fatalf("invalid metadata = %+v, %v", inventory, err)
	}
}

func TestReadRepositoryCandidatesRejectsSymlinkedEvidence(t *testing.T) {
	root := t.TempDir()
	target := writeCandidate(t, root, "app-misc", "target-1", "EAPI=8\nSLOT=0\n")
	link := filepath.Join(filepath.Dir(target), "linked-1")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	inventory, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Candidates) != 1 || len(inventory.Issues) != 1 ||
		inventory.Issues[0].Code != IssueInvalidMetadata || inventory.Issues[0].Path != link {
		t.Fatalf("symlink evidence = %+v", inventory)
	}
}

func TestReadRepositoryCandidatesDetectsConcurrentMutation(t *testing.T) {
	root := t.TempDir()
	path := writeCandidate(t, root, "app-misc", "example-1", "EAPI=8\nSLOT=0\n")
	inventory, err := ReadRepositoryCandidates(context.Background(), []Repository{{Name: "test", Location: root}}, CandidateOptions{
		afterMetadataRead: func(got string) {
			if got == path {
				if writeErr := os.WriteFile(path, []byte("EAPI=8\nSLOT=1\nKEYWORDS=amd64\n"), 0o644); writeErr != nil {
					t.Error(writeErr)
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Candidates) != 0 || len(inventory.Issues) != 1 ||
		inventory.Issues[0].Code != IssueConcurrentMutation ||
		!errors.Is(inventory.Issues[0], ErrConcurrentMutation) {
		t.Fatalf("mutation evidence = %+v", inventory)
	}
}

func TestReadRepositoryCandidatesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadRepositoryCandidates(ctx, nil, CandidateOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func BenchmarkReadRepositoryCandidates(b *testing.B) {
	root := b.TempDir()
	for index := 0; index < 250; index++ {
		path := filepath.Join(root, "metadata", "md5-cache", "app-misc", "example-"+strconv.Itoa(index+1))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("EAPI=8\nSLOT=0\nKEYWORDS=amd64\nIUSE=+feature test\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	repositories := []Repository{{Name: "test", Location: root}}
	b.ResetTimer()
	for range b.N {
		if _, err := ReadRepositoryCandidates(context.Background(), repositories, CandidateOptions{Integrity: RequireComplete}); err != nil {
			b.Fatal(err)
		}
	}
}
