package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

type testTB interface {
	Helper()
	Fatal(...any)
}

func writeInstalled(t testTB, root, cpv string, overrides map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(cpv))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"CONTENTS": "obj /usr/bin/example digest 1\n", "EAPI": "8\n", "SLOT": "0/1\n",
		"repository": "gentoo\n", "USE": "ssl test\n", "IUSE": "+ssl test\n",
		"DEPEND": "dev-libs/a\n", "RDEPEND": "dev-libs/b\n", "BUILD_TIME": "1700000000\n", "COUNTER": "42\n",
	}
	for name, value := range overrides {
		files[name] = value
	}
	for name, value := range files {
		if value == "<absent>" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadInstalledReturnsSortedOwnedInventory(t *testing.T) {
	root := t.TempDir()
	writeInstalled(t, root, "dev-libs/zeta-2", nil)
	writeInstalled(t, root, "app-misc/alpha-1", nil)
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: RequireComplete, IncludeContents: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 2 || got.Packages[0].ID.CPV() != "app-misc/alpha-1" {
		t.Fatalf("packages are not deterministic: %+v", got.Packages)
	}
	pkg := got.Packages[1]
	if pkg.ID.Slot != "0" || pkg.ID.Subslot != "1" || pkg.ID.Repository != "gentoo" || pkg.Build.Time != 1700000000 || pkg.Build.Counter != 42 || pkg.Contents == "" {
		t.Fatalf("metadata lost: %+v", pkg)
	}
	wantDeclarations := []UseDeclaration{
		{Name: "ssl", Default: UseDefaultEnabled},
		{Name: "test", Default: UseDefaultUnspecified},
	}
	if !reflect.DeepEqual(pkg.DeclaredUse, wantDeclarations) {
		t.Fatalf("IUSE declarations = %+v, want %+v", pkg.DeclaredUse, wantDeclarations)
	}
	if got := []string{pkg.DeclaredUse[0].String(), pkg.DeclaredUse[1].String()}; !reflect.DeepEqual(got, []string{"+ssl", "test"}) {
		t.Fatalf("rendered IUSE declarations = %v", got)
	}
}

func TestReadInstalledRejectsUnknownOptions(t *testing.T) {
	root := t.TempDir()
	for name, options := range map[string]InstalledOptions{
		"integrity": {Integrity: IntegrityMode(255)},
		"workers":   {Workers: -1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, options)
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadInstalledPartialAndStrictIntegrity(t *testing.T) {
	root := t.TempDir()
	writeInstalled(t, root, "app-misc/good-1", nil)
	bad := writeInstalled(t, root, "app-misc/partial-1", map[string]string{"CONTENTS": "<absent>"})

	partial, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: AllowPartial})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Packages) != 1 || len(partial.Issues) != 1 || partial.Issues[0].Code != IssueInterruptedRecord {
		t.Fatalf("unexpected partial result: %+v", partial)
	}
	if partial.Issues[0].Path != filepath.Join(bad, "CONTENTS") || !errors.Is(partial.Issues[0], ErrInterruptedRecord) {
		t.Fatalf("issue lacks typed evidence: %+v", partial.Issues[0])
	}

	strict, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: RequireComplete})
	if !errors.Is(err, ErrIncompleteEvidence) || len(strict.Packages) != 1 || len(strict.Issues) != 1 {
		t.Fatalf("strict result = %+v, error = %v", strict, err)
	}
	var integrity *IntegrityError
	if !errors.As(err, &integrity) || len(integrity.Issues) != 1 {
		t.Fatalf("typed integrity error missing: %v", err)
	}
}

func TestReadInstalledDiagnosesCorruptCommittedMetadata(t *testing.T) {
	root := t.TempDir()
	dir := writeInstalled(t, root, "app-misc/example-1", map[string]string{"SLOT": ""})
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 0 || len(got.Issues) != 1 || got.Issues[0].Code != IssueCorruptRecord || !errors.Is(got.Issues[0], ErrCorruptRecord) {
		t.Fatalf("corruption not diagnosed: %+v", got)
	}
	if got.Issues[0].Path != filepath.Join(dir, "SLOT") {
		t.Fatalf("wrong issue path: %q", got.Issues[0].Path)
	}
}

func TestReadInstalledDiagnosesInvalidOptionalInteger(t *testing.T) {
	root := t.TempDir()
	writeInstalled(t, root, "app-misc/example-1", map[string]string{"COUNTER": "not-an-integer\n"})
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 1 || len(got.Issues) != 1 || got.Issues[0].Code != IssueInvalidMetadata {
		t.Fatalf("invalid integer silently accepted: %+v", got)
	}
	if !errors.Is(got.Issues[0], ErrInvalidData) {
		t.Fatalf("invalid-data category missing: %+v", got.Issues[0])
	}
}

func TestReadInstalledRejectsSymlinkedRequiredMetadata(t *testing.T) {
	root := t.TempDir()
	dir := writeInstalled(t, root, "app-misc/example-1", nil)
	if err := os.Remove(filepath.Join(dir, "CONTENTS")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "CONTENTS")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "CONTENTS")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != IssueCorruptRecord {
		t.Fatalf("symlink accepted: %+v", got)
	}
}

func TestReadInstalledRejectsSymlinkedOptionalMetadata(t *testing.T) {
	root := t.TempDir()
	dir := writeInstalled(t, root, "app-misc/example-1", nil)
	if err := os.Remove(filepath.Join(dir, "DEPEND")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "DEPEND")
	if err := os.WriteFile(outside, []byte("dev-libs/escaped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "DEPEND")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 1 || got.Packages[0].Dependencies.Depend != "" {
		t.Fatalf("optional symlink data escaped containment: %+v", got.Packages)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != IssueCorruptRecord {
		t.Fatalf("optional symlink lacks typed issue: %+v", got.Issues)
	}
}

func TestReadInstalledDoesNotReadContentsUnlessRequested(t *testing.T) {
	root := t.TempDir()
	dir := writeInstalled(t, root, "app-misc/example-1", nil)
	contents := filepath.Join(dir, "CONTENTS")
	if err := os.Chmod(contents, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(contents, 0o644) })

	without, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: RequireComplete})
	if err != nil || len(without.Packages) != 1 || without.Packages[0].Contents != "" {
		t.Fatalf("identity-only scan read CONTENTS: inventory=%+v error=%v", without, err)
	}
	_, err = ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: RequireComplete, IncludeContents: true})
	if !errors.Is(err, ErrIncompleteEvidence) {
		t.Fatalf("CONTENTS read error = %v", err)
	}
}

func TestReadInstalledDetectsConcurrentRecordMutation(t *testing.T) {
	root := t.TempDir()
	dir := writeInstalled(t, root, "app-misc/example-1", nil)
	var once sync.Once
	options := InstalledOptions{
		Integrity: RequireComplete,
		afterRequiredSnapshot: func(record string) {
			once.Do(func() {
				if err := os.WriteFile(filepath.Join(record, "SLOT"), []byte("changed-slot\n"), 0o644); err != nil {
					t.Error(err)
				}
			})
		},
	}
	got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, options)
	if !errors.Is(err, ErrIncompleteEvidence) || len(got.Packages) != 0 {
		t.Fatalf("mutating scan inventory=%+v error=%v", got, err)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != IssueConcurrentMutation ||
		!errors.Is(got.Issues[0], ErrConcurrentMutation) ||
		got.Issues[0].Path != filepath.Join(dir, "SLOT") {
		t.Fatalf("mutation issue = %+v", got.Issues)
	}
}

func TestReadInstalledUsesBoundedConcurrentWorkers(t *testing.T) {
	root := t.TempDir()
	writeInstalled(t, root, "app-misc/one-1", nil)
	writeInstalled(t, root, "app-misc/two-1", nil)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{
			Integrity: RequireComplete,
			Workers:   2,
			beforeRecordRead: func(string) {
				entered <- struct{}{}
				<-release
			},
		})
		done <- err
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("record reads did not run concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReadInstalledOrderingIsIndependentOfWorkerCount(t *testing.T) {
	root := t.TempDir()
	for _, cpv := range []string{"dev-libs/zeta-2", "app-misc/alpha-1", "sys-apps/middle-3"} {
		writeInstalled(t, root, cpv, nil)
	}
	var baseline []string
	for _, workers := range []int{1, 2, 8} {
		got, err := ReadInstalled(context.Background(), SystemPaths{VDB: root}, InstalledOptions{Integrity: RequireComplete, Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		var identities []string
		for _, pkg := range got.Packages {
			identities = append(identities, pkg.ID.CPV())
		}
		if baseline == nil {
			baseline = identities
		} else if !reflect.DeepEqual(identities, baseline) {
			t.Fatalf("workers=%d identities=%v baseline=%v", workers, identities, baseline)
		}
	}
}

func TestReadInstalledHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadInstalled(ctx, SystemPaths{VDB: t.TempDir()}, InstalledOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestReadInstalledRequiredRootErrorsPreserveFilesystemCategory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := ReadInstalled(context.Background(), SystemPaths{VDB: missing}, InstalledOptions{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v", err)
	}
}

func BenchmarkReadInstalledResolverInventory(b *testing.B) {
	root := b.TempDir()
	for _, cpv := range []string{
		"app-misc/alpha-1", "dev-libs/bravo-2.1", "dev-lang/charlie-3.2-r1",
		"sys-apps/delta-4", "sys-libs/echo-5.1", "virtual/foxtrot-6",
	} {
		writeInstalled(b, root, cpv, nil)
	}
	paths := SystemPaths{VDB: root}
	b.ResetTimer()
	for range b.N {
		inventory, err := ReadInstalled(context.Background(), paths, InstalledOptions{Integrity: RequireComplete})
		if err != nil || len(inventory.Packages) != 6 {
			b.Fatalf("inventory packages = %d, error = %v", len(inventory.Packages), err)
		}
	}
}

func BenchmarkReadInstalledLargeInventory(b *testing.B) {
	root := b.TempDir()
	for index := range 512 {
		cpv := "app-misc/example-" + strconv.Itoa(index) + "-1"
		writeInstalled(b, root, cpv, nil)
	}
	paths := SystemPaths{VDB: root}
	for _, workers := range []int{1, 0} {
		name := "serial"
		if workers == 0 {
			name = "default"
		}
		b.Run(name, func(b *testing.B) {
			for range b.N {
				inventory, err := ReadInstalled(context.Background(), paths, InstalledOptions{
					Integrity: RequireComplete,
					Workers:   workers,
				})
				if err != nil || len(inventory.Packages) != 512 {
					b.Fatalf("inventory packages = %d, error = %v", len(inventory.Packages), err)
				}
			}
		})
	}
}
