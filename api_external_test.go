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
