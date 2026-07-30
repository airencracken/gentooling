package gentooling

import (
	"path/filepath"
	"testing"
)

func TestDefaultSystemPathsAreRootRelative(t *testing.T) {
	root := t.TempDir()
	paths := DefaultSystemPaths(root)
	for _, path := range []string{paths.ConfigRoot, paths.VDB, paths.World, paths.MakeGlobals, paths.ActiveProfile} {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("path %q escaped root %q", path, root)
		}
	}
}
