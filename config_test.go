package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func effectiveConfigFixture(t *testing.T) SystemPaths {
	t.Helper()
	paths, _, leaf := profileFixture(t)
	root := filepath.Dir(filepath.Dir(paths.ActiveProfile))
	paths.ConfigRoot = filepath.Join(root, "etc", "portage")
	paths.MakeGlobals = filepath.Join(root, "usr", "share", "portage", "config", "make.globals")
	writeProfileFile(t, filepath.Dir(paths.MakeGlobals), "make.globals", "FEATURES=\"sandbox\"\nUSE_EXPAND=\"PYTHON_TARGETS\"\n")
	writeProfileFile(t, leaf, "make.defaults", "USE=\"profile\"\nPYTHON_TARGETS=\"python3_13\"\nCFLAGS=\"-O1\"\n")
	writeProfileFile(t, paths.ConfigRoot, "make.conf", "USE=\"user -profile\"\nPYTHON_TARGETS=\"python3_14 -python3_13\"\nCFLAGS=\"${CFLAGS} -O2\"\n")
	writeProfileFile(t, filepath.Join(paths.ConfigRoot, "package.use"), "10-base", "app-misc/example feature\n")
	writeProfileFile(t, filepath.Join(paths.ConfigRoot, "package.use"), "20-local", "app-misc/example -feature local\n")
	return paths
}

func TestReadEffectiveConfigPreservesLayersExpansionAndProvenance(t *testing.T) {
	paths := effectiveConfigFixture(t)
	got, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{
		Environment: []string{"USE=command", "PYTHON_TARGETS=python3_15", "SECRET=ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile == nil || len(got.Profile.Layers) != 2 {
		t.Fatalf("profile graph missing: %+v", got.Profile)
	}
	if got.Variables["CFLAGS"] != "-O1 -O2" || got.Variables["SECRET"] != "" {
		t.Fatalf("effective variables = %+v", got.Variables)
	}
	if !reflect.DeepEqual(got.UseExpand, []string{"PYTHON_TARGETS"}) {
		t.Fatalf("USE_EXPAND = %v", got.UseExpand)
	}
	wantProfile := []string{"base", "profile", "python_targets_python3_13"}
	if names := changeNames(got.ProfileUse); !reflect.DeepEqual(names, wantProfile) {
		t.Fatalf("profile USE = %v, want %v", names, wantProfile)
	}
	wantUser := []string{"user", "-profile", "python_targets_python3_14", "-python_targets_python3_13"}
	if names := changeNames(got.UserUse); !reflect.DeepEqual(names, wantUser) {
		t.Fatalf("user USE = %v, want %v", names, wantUser)
	}
	if names := changeNames(got.CommandUse); !reflect.DeepEqual(names, []string{"command", "python_targets_python3_15"}) {
		t.Fatalf("command USE = %v", names)
	}
	if len(got.UserPackageUse) != 2 || got.UserPackageUse[0].Source.Line != 1 ||
		filepath.Base(got.UserPackageUse[0].Source.Path) != "10-base" ||
		filepath.Base(got.UserPackageUse[1].Source.Path) != "20-local" {
		t.Fatalf("package.use ordering/provenance = %+v", got.UserPackageUse)
	}
}

func TestReadEffectiveConfigAcceptsCanonicalMultilineGlobals(t *testing.T) {
	paths := effectiveConfigFixture(t)
	writeProfileFile(t, filepath.Dir(paths.MakeGlobals), "make.globals", `FEATURES="
        sandbox
        userpriv
        usersandbox
"
USE_EXPAND="PYTHON_TARGETS"
ACCEPT_KEYWORDS="amd64 \
~amd64"
LINK_FLAGS="-O2 -pipe -fno-\
plt"
ac_cv_c_undeclared_builtin_options="none needed"
`)
	got, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if fields := strings.Fields(got.Variables["FEATURES"]); !reflect.DeepEqual(fields, []string{"sandbox", "userpriv", "usersandbox"}) {
		t.Fatalf("FEATURES = %q (%v)", got.Variables["FEATURES"], fields)
	}
	if fields := strings.Fields(got.Variables["ACCEPT_KEYWORDS"]); !reflect.DeepEqual(fields, []string{"amd64", "~amd64"}) {
		t.Fatalf("ACCEPT_KEYWORDS = %q (%v)", got.Variables["ACCEPT_KEYWORDS"], fields)
	}
	if got.Variables["LINK_FLAGS"] != "-O2 -pipe -fno-plt" {
		t.Fatalf("LINK_FLAGS = %q", got.Variables["LINK_FLAGS"])
	}
	if got.Variables["ac_cv_c_undeclared_builtin_options"] != "none needed" {
		t.Fatalf("lowercase cache variable = %q", got.Variables["ac_cv_c_undeclared_builtin_options"])
	}
}

func TestReadEffectiveConfigRejectsUnterminatedMultilineAssignments(t *testing.T) {
	for name, value := range map[string]string{
		"quote":        "FEATURES=\"sandbox\n",
		"continuation": "FEATURES=sandbox \\\n",
	} {
		t.Run(name, func(t *testing.T) {
			paths := effectiveConfigFixture(t)
			writeProfileFile(t, filepath.Dir(paths.MakeGlobals), "make.globals", value)
			_, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadEffectiveConfigDoesNotReadProcessEnvironment(t *testing.T) {
	paths := effectiveConfigFixture(t)
	t.Setenv("USE", "must-not-enter")
	got, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range append(append([]FlagChange(nil), got.ProfileUse...), got.UserUse...) {
		if change.Name == "must-not-enter" {
			t.Fatal("process environment entered effective configuration")
		}
	}
}

func TestReadEffectiveConfigExpandsAgainstPriorEnvironmentOnly(t *testing.T) {
	paths := effectiveConfigFixture(t)
	writeProfileFile(t, filepath.Dir(paths.MakeGlobals), "make.globals", `SELF="${SELF} first"
FORWARD="${LATER} before"
LATER="after"
SELF="${SELF} second"
`)
	got, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Variables["SELF"] != " first second" || got.Variables["FORWARD"] != " before" {
		t.Fatalf("shell-order expansion = SELF %q, FORWARD %q", got.Variables["SELF"], got.Variables["FORWARD"])
	}
}

func TestReadEffectiveConfigRejectsMissingGlobalsAndSymlinkedUserConfig(t *testing.T) {
	paths := effectiveConfigFixture(t)
	if err := os.Remove(paths.MakeGlobals); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing globals error = %v", err)
	}

	paths = effectiveConfigFixture(t)
	makeConf := filepath.Join(paths.ConfigRoot, "make.conf")
	if err := os.Remove(makeConf); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "make.conf")
	if err := os.WriteFile(outside, []byte("USE=escaped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, makeConf); err != nil {
		t.Fatal(err)
	}
	_, err = ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("symlinked make.conf error = %v", err)
	}
}

func TestReadEffectiveConfigRejectsSymlinkedPackageUseEntry(t *testing.T) {
	paths := effectiveConfigFixture(t)
	outside := filepath.Join(t.TempDir(), "package.use")
	if err := os.WriteFile(outside, []byte("app-misc/example escaped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(paths.ConfigRoot, "package.use", "30-escaped")); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("symlinked package.use entry error = %v", err)
	}
}

func TestReadEffectiveConfigHonorsCancellationAndOwnsResults(t *testing.T) {
	paths := effectiveConfigFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadEffectiveConfig(ctx, paths, ConfigOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	first, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first.Variables["USE"] = "mutated"
	first.UserPackageUse[0].Flags[0] = "mutated"
	second, err := ReadEffectiveConfig(context.Background(), paths, ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Variables["USE"] == "mutated" || second.UserPackageUse[0].Flags[0] == "mutated" {
		t.Fatal("caller mutation leaked across configuration loads")
	}
}

func changeNames(changes []FlagChange) []string {
	result := make([]string, 0, len(changes))
	for _, change := range changes {
		name := change.Name
		if !change.Enabled {
			name = "-" + name
		}
		result = append(result, name)
	}
	return result
}
