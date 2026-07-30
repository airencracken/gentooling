package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadSelectionsReturnsWorldSetsAtomsAndEffectiveSystem(t *testing.T) {
	paths, _, leaf := profileFixture(t)
	paths.World = filepath.Join(filepath.Dir(filepath.Dir(paths.ActiveProfile)), "var", "lib", "portage", "world")
	writeProfileFile(t, filepath.Dir(paths.World), "world", "# chosen packages\n@desktop\napp-editors/vim\n>=sys-kernel/gentoo-sources-6.12\napp-editors/vim\n")

	got, err := ReadSelections(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if values := selectionValues(got.World); !reflect.DeepEqual(values, []string{">=sys-kernel/gentoo-sources-6.12", "@desktop", "app-editors/vim"}) {
		t.Fatalf("world = %v", values)
	}
	if got.World[1].Kind != SetSelection || got.World[1].Atom != nil {
		t.Fatalf("set selection = %+v", got.World[1])
	}
	if values := selectionValues(got.System); !reflect.DeepEqual(values, []string{"sys-apps/openrc", "sys-libs/glibc"}) {
		t.Fatalf("system = %v", values)
	}
	openrc := got.System[0]
	if openrc.Source.Path != filepath.Join(leaf, "packages") || openrc.Source.Line != 2 {
		t.Fatalf("system provenance = %+v", openrc.Source)
	}
}

func TestReadSelectionsMissingWorldIsEmptyAndSymlinkIsRejected(t *testing.T) {
	paths, _, _ := profileFixture(t)
	paths.World = filepath.Join(t.TempDir(), "missing-world")
	got, err := ReadSelections(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.World) != 0 {
		t.Fatalf("missing world = %+v", got.World)
	}

	target := filepath.Join(t.TempDir(), "world")
	if err := os.WriteFile(target, []byte("app-editors/vim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.World); err != nil {
		t.Fatal(err)
	}
	_, err = ReadSelections(context.Background(), paths)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("symlink world error = %v", err)
	}
}

func TestReadSelectionsRejectsMalformedEntries(t *testing.T) {
	for _, value := range []string{"not-an-atom", "@", "@bad/name", "app-editors/vim trailing"} {
		t.Run(value, func(t *testing.T) {
			paths, _, _ := profileFixture(t)
			paths.World = filepath.Join(t.TempDir(), "world")
			if err := os.WriteFile(paths.World, []byte(value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ReadSelections(context.Background(), paths)
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadSelectionsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadSelections(ctx, SystemPaths{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func selectionValues(selections []Selection) []string {
	values := make([]string, len(selections))
	for index, selection := range selections {
		values[index] = selection.Value
	}
	return values
}
