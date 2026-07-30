package gentooling

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrInvalidData = errors.New("gentooling: invalid data")

var (
	categoryPattern = regexp.MustCompile(`^[A-Za-z0-9+_.-]+$`)
	packagePattern  = regexp.MustCompile(`^[A-Za-z0-9+_][A-Za-z0-9+_.-]*[A-Za-z0-9+_]$|^[A-Za-z0-9+_]$`)
	versionPattern  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*[a-z]?(?:_(?:alpha|beta|pre|rc|p)[0-9]*)*(?:-r[0-9]+)?$`)
)

// PackageID is the stable identity of one package version.
type PackageID struct {
	Category   string
	Name       string
	Version    string
	Slot       string
	Subslot    string
	Repository string
}

func (p PackageID) CP() string {
	return p.Category + "/" + p.Name
}

func (p PackageID) CPV() string {
	return p.CP() + "-" + p.Version
}

// ParsePackageID parses a category/package-version identity. It deliberately
// does not accept dependency atom operators.
func ParsePackageID(value string) (PackageID, error) {
	category, pf, ok := strings.Cut(value, "/")
	if !ok || !categoryPattern.MatchString(category) || pf == "" || strings.Contains(pf, "/") {
		return PackageID{}, fmt.Errorf("%w: package identity %q", ErrInvalidData, value)
	}
	for index := 0; index < len(pf)-1; index++ {
		if pf[index] != '-' || !asciiDigit(pf[index+1]) {
			continue
		}
		name, version := pf[:index], pf[index+1:]
		if !packagePattern.MatchString(name) || !versionPattern.MatchString(version) {
			continue
		}
		return PackageID{Category: category, Name: name, Version: version}, nil
	}
	return PackageID{}, fmt.Errorf("%w: package identity %q has no version", ErrInvalidData, value)
}

func asciiDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
