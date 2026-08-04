package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadPreservedLibrariesValidatesAndRebasesRecords(t *testing.T) {
	root := t.TempDir()
	registry := filepath.Join(root, "var/lib/portage/preserved_libs_registry")
	if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"dev-libs/example:0":["dev-libs/example-2.1","7",["/usr/lib/libexample.so.1","/usr/lib/libexample.so.1","/lib/libother.so"]]}`
	if err := os.WriteFile(registry, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := ReadPreservedLibraries(context.Background(), root, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Owner.CPV() != "dev-libs/example-2.1" || records[0].Counter != "7" || len(records[0].Paths) != 2 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].RootedPaths[1] != filepath.Join(root, "usr/lib/libexample.so.1") {
		t.Fatalf("rooted paths = %#v", records[0].RootedPaths)
	}
}

func TestReadPreservedLibrariesAcceptsAbsentAndEmptyFreshStage3State(t *testing.T) {
	root := t.TempDir()
	registry := filepath.Join(root, "registry")
	for _, contents := range []string{"", " \n\t"} {
		if err := os.WriteFile(registry, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		records, err := ReadPreservedLibraries(context.Background(), root, registry)
		if err != nil || len(records) != 0 {
			t.Fatalf("empty state = %#v, %v", records, err)
		}
	}
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	records, err := ReadPreservedLibraries(context.Background(), root, registry)
	if err != nil || len(records) != 0 {
		t.Fatalf("absent state = %#v, %v", records, err)
	}
}

func TestReadPreservedLibrariesRejectsMalformedAndUnsafeState(t *testing.T) {
	root := t.TempDir()
	registry := filepath.Join(root, "registry")
	for _, data := range []string{"{", `{"bad":["dev-libs/example-1","1"]}`, `{"bad":["dev-libs/example-1","1",["../escape"]]}`, `{"bad":["not-an-owner","1",["/lib/x"]]}`} {
		if err := os.WriteFile(registry, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPreservedLibraries(context.Background(), root, registry); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("data %q error = %v", data, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadPreservedLibraries(ctx, root, registry); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}
