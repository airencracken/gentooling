package gentooling

import (
	"errors"
	"testing"
)

func TestParsePackageID(t *testing.T) {
	got, err := ParsePackageID("dev-libs/libfoo-bar-1.2.3-r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != "dev-libs" || got.Name != "libfoo-bar" || got.Version != "1.2.3-r1" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.CP() != "dev-libs/libfoo-bar" || got.CPV() != "dev-libs/libfoo-bar-1.2.3-r1" {
		t.Fatalf("unexpected rendering: CP=%q CPV=%q", got.CP(), got.CPV())
	}
}

func TestParsePackageIDRejectsAdversarialInputs(t *testing.T) {
	for _, value := range []string{"", "dev-libs", "/foo-1", "dev-libs/-1", "dev-libs/foo", "dev-libs/foo-x", "dev-libs/foo-1/other"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParsePackageID(value); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func FuzzParsePackageIDNeverPanics(f *testing.F) {
	for _, value := range []string{"dev-libs/libfoo-1", "sys-kernel/gentoo-sources-6.18.1-r2", "", "////", "a/b-1_rc1"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		id, err := ParsePackageID(value)
		if err == nil && id.CPV() != value {
			t.Fatalf("round trip changed %q to %q", value, id.CPV())
		}
	})
}
