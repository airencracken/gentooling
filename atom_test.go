package gentooling

import (
	"errors"
	"testing"
)

func TestAtomRoundTripAndMatching(t *testing.T) {
	raw := ">=sys-devel/gcc-14.2.0-r1:14/14.2=::gentoo[cxx,-jit]"
	parsed, err := ParseAtom(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.String(); got != raw {
		t.Fatalf("String() = %q, want %q", got, raw)
	}
	matched, err := parsed.Matches(PackageID{
		Category: "sys-devel", Name: "gcc", Version: "14.2.1",
		Slot: "14", Subslot: "14.2", Repository: "gentoo",
	}, UseState{
		Declared: map[string]bool{"cxx": true, "jit": true},
		Enabled:  map[string]bool{"cxx": true},
	})
	if err != nil || !matched {
		t.Fatalf("Matches() = %v, %v", matched, err)
	}
}

func TestAtomNoMatchIsNotAnError(t *testing.T) {
	parsed, err := ParseAtom("=dev-libs/example-2")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := parsed.Matches(PackageID{Category: "dev-libs", Name: "example", Version: "1"}, UseState{})
	if err != nil || matched {
		t.Fatalf("Matches() = %v, %v", matched, err)
	}
}

func TestAtomUseDependencyDefaultsAndConditionals(t *testing.T) {
	parsed, err := ParseAtom("dev-libs/example[ssl(+),test?,!debug=]")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := parsed.Matches(
		PackageID{Category: "dev-libs", Name: "example", Version: "1"},
		UseState{
			Declared: map[string]bool{"test": true, "debug": true},
			Enabled:  map[string]bool{"test": true},
			Caller:   map[string]bool{"test": true, "debug": true},
		},
	)
	if err != nil || !matched {
		t.Fatalf("Matches() = %v, %v", matched, err)
	}
}

func TestVersionOrderingVectors(t *testing.T) {
	vectors := []struct{ older, newer string }{
		{"1.0_alpha", "1.0_beta"},
		{"1.0_beta", "1.0_pre"},
		{"1.0_pre", "1.0_rc"},
		{"1.0_rc", "1.0"},
		{"1.0", "1.0_p1"},
		{"1.0", "1.0-r1"},
		{"1.01", "1.1"},
		{"1.0", "1.0.0"},
		{"999999999999999999999999", "1000000000000000000000000"},
	}
	for _, vector := range vectors {
		left, err := ParseVersion(vector.older)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", vector.older, err)
		}
		right, err := ParseVersion(vector.newer)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", vector.newer, err)
		}
		if got := left.Compare(right); got >= 0 {
			t.Errorf("%s.Compare(%s) = %d, want < 0", vector.older, vector.newer, got)
		}
		if got := right.Compare(left); got <= 0 {
			t.Errorf("%s.Compare(%s) = %d, want > 0", vector.newer, vector.older, got)
		}
	}
}

func TestAtomRejectsAdversarialInputs(t *testing.T) {
	inputs := []string{
		"", " dev-libs/foo", "dev-libs/foo ", "../foo", "./foo",
		"dev-libs/foo-1", ">dev-libs/foo", ">dev-libs/foo-1*",
		"dev-libs/foo[]", "dev-libs/foo[x,x]", "dev-libs/foo[!x]",
		"dev-libs/foo:1/*", "dev-libs/foo::../repo",
	}
	for _, input := range inputs {
		_, err := ParseAtom(input)
		if !errors.Is(err, ErrInvalidData) {
			t.Errorf("ParseAtom(%q) error = %v, want ErrInvalidData", input, err)
		}
	}
}

func FuzzParseAtomNeverPanics(f *testing.F) {
	for _, seed := range []string{"dev-libs/foo", "=dev-libs/foo-1", "cat/pkg:0::repo[use]"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := ParseAtom(input)
		if err == nil {
			if _, reparsedErr := ParseAtom(parsed.String()); reparsedErr != nil {
				t.Fatalf("round trip %q: %v", parsed.String(), reparsedErr)
			}
		}
	})
}
