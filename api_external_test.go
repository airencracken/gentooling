package gentooling_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestInstalledInventoryPublicContract(t *testing.T) {
	paths := gentooling.DefaultSystemPaths(t.TempDir())
	_, err := gentooling.ReadInstalled(context.Background(), paths, gentooling.InstalledOptions{
		Integrity: gentooling.RequireComplete,
		Workers:   2,
	})
	if err == nil {
		t.Fatal("missing fixture VDB unexpectedly succeeded")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapped error contract is not discoverable: %v", err)
	}

	declaration := gentooling.UseDeclaration{Name: "ssl", Default: gentooling.UseDefaultEnabled}
	if declaration.Name != "ssl" {
		t.Fatalf("public declaration contract changed: %+v", declaration)
	}
}

func TestProfilePublicContract(t *testing.T) {
	_, err := gentooling.ReadProfile(context.Background(), gentooling.SystemPaths{
		ActiveProfile: "/definitely/missing/make.profile",
		Repositories: []gentooling.RepositoryPath{
			{Name: "gentoo", Path: "/var/db/repos/gentoo"},
		},
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile filesystem error is not discoverable: %v", err)
	}

	rule := gentooling.PackageFlagRule{
		Atom:   "app-misc/example",
		Flags:  []string{"feature"},
		Source: gentooling.PolicySource{Path: "/repo/profiles/package.use", Line: 4},
	}
	if rule.Source.Line != 4 {
		t.Fatalf("public profile provenance changed: %+v", rule)
	}
}
