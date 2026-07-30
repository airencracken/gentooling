package gentooling

import "path/filepath"

// SystemPaths names every host location a Gentooling operation may inspect.
// Callers may use DefaultSystemPaths for a live system or provide fixture and
// alternate-root paths explicitly.
type SystemPaths struct {
	Root          string
	ConfigRoot    string
	VDB           string
	World         string
	MakeGlobals   string
	Repositories  []RepositoryPath
	ActiveProfile string
}

type RepositoryPath struct {
	Name string
	// Path is the repository root containing profiles/, metadata/, and ebuilds.
	Path string
}

func DefaultSystemPaths(root string) SystemPaths {
	clean := filepath.Clean(root)
	return SystemPaths{
		Root:          clean,
		ConfigRoot:    filepath.Join(clean, "etc", "portage"),
		VDB:           filepath.Join(clean, "var", "db", "pkg"),
		World:         filepath.Join(clean, "var", "lib", "portage", "world"),
		MakeGlobals:   filepath.Join(clean, "usr", "share", "portage", "config", "make.globals"),
		ActiveProfile: filepath.Join(clean, "etc", "portage", "make.profile"),
	}
}

// PortageStateLockPath mirrors portage.locks.lockdir. A state path or
// directory is protected by a sibling .<basename>.portage_lockfile.
func PortageStateLockPath(path string) string {
	path = filepath.Clean(path)
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".portage_lockfile")
}
