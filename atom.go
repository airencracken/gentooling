package gentooling

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Op is a Gentoo package atom version operator.
type Op string

const (
	OpNone   Op = ""
	OpLess   Op = "<"
	OpLessEq Op = "<="
	OpEq     Op = "="
	OpEqGlob Op = "=*"
	OpTilde  Op = "~"
	OpGtEq   Op = ">="
	OpGt     Op = ">"
)

// SlotOp describes the rebuild semantics attached to an atom slot.
type SlotOp string

const (
	SlotOpNone SlotOp = ""
	SlotOpEq   SlotOp = "="
	SlotOpStar SlotOp = "*"
)

// Version is a parsed Gentoo package version.
type Version struct {
	Raw      string
	Revision int
	base     string
}

// UseDependency is one USE constraint attached to an atom.
type UseFlag struct {
	Name        string
	Enabled     bool
	Conditional bool
	Equal       bool
	Negated     bool
	Default     *bool
}

// Atom is a parsed Gentoo package dependency atom.
type Atom struct {
	Op       Op
	Category string
	Package  string
	Version  *Version
	Slot     string
	Subslot  string
	SlotOp   SlotOp
	Repo     string
	UseFlags []UseFlag
}

// UseState contains both enabled and declared USE state. Caller is used to
// evaluate conditional and equality dependencies.
type UseState struct {
	Enabled  map[string]bool
	Declared map[string]bool
	Caller   map[string]bool
}

var (
	atomCategoryPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9+_.-]*$`)
	atomPackagePattern  = regexp.MustCompile(`^[A-Za-z0-9+_][A-Za-z0-9+_.-]*$`)
	atomRepoPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)
	atomSlotPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9+_.-]*$`)
	atomVersionPattern  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*[a-z]?(?:_(?:alpha|beta|pre|rc|p)[0-9]*)*(?:-r[0-9]+)?$`)
	versionSplitPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)([a-z]?)(.*)$`)
	suffixPattern       = regexp.MustCompile(`^_(alpha|beta|pre|rc|p)([0-9]*)(.*)$`)
)

// ParseAtom parses a package dependency atom. A version requires an operator.
func ParseAtom(raw string) (Atom, error) {
	atom, err := parseAtom(raw, false)
	if err != nil {
		return Atom{}, err
	}
	if atom.Version != nil && atom.Op == OpNone {
		return Atom{}, fmt.Errorf("%w: versioned package atom %q requires an operator", ErrInvalidData, raw)
	}
	return atom, nil
}

// ParsePackageAtom is the pointer-returning compatibility form of ParseAtom.
func ParsePackageAtom(raw string) (*Atom, error) {
	parsed, err := ParseAtom(raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// Parse accepts either a package atom or a bare CPV identity.
func Parse(raw string) (*Atom, error) {
	parsed, err := parseAtom(raw, true)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// ParsePackageVersion parses a CPV. Unlike ParseAtom, it accepts a version
// without a dependency operator.
func ParsePackageVersion(raw string) (Atom, error) {
	return parseAtom(raw, true)
}

// ParseVersion parses a Gentoo version.
func ParseVersion(raw string) (*Version, error) {
	if !atomVersionPattern.MatchString(raw) {
		return nil, fmt.Errorf("%w: version %q", ErrInvalidData, raw)
	}
	revision := 0
	base := raw
	if index := strings.LastIndex(raw, "-r"); index >= 0 {
		parsed, err := strconv.Atoi(raw[index+2:])
		if err != nil {
			return nil, fmt.Errorf("%w: version %q revision", ErrInvalidData, raw)
		}
		revision, base = parsed, raw[:index]
	}
	return &Version{Raw: raw, Revision: revision, base: base}, nil
}

func parseAtom(raw string, allowBareVersion bool) (Atom, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return Atom{}, fmt.Errorf("%w: atom %q", ErrInvalidData, raw)
	}
	for index := range len(raw) {
		if raw[index] < 0x21 || raw[index] > 0x7e {
			return Atom{}, fmt.Errorf("%w: atom %q contains invalid byte", ErrInvalidData, raw)
		}
	}
	result := Atom{}
	for _, candidate := range []struct {
		text string
		op   Op
	}{{"<=", OpLessEq}, {">=", OpGtEq}, {"<", OpLess}, {">", OpGt}, {"~", OpTilde}, {"=", OpEq}} {
		if strings.HasPrefix(raw, candidate.text) {
			result.Op, raw = candidate.op, raw[len(candidate.text):]
			break
		}
	}
	if strings.HasPrefix(raw, "!") {
		return Atom{}, fmt.Errorf("%w: blocker is a dependency expression, not an atom", ErrInvalidData)
	}
	if open := strings.IndexByte(raw, '['); open >= 0 {
		if !strings.HasSuffix(raw, "]") {
			return Atom{}, fmt.Errorf("%w: unterminated USE dependency", ErrInvalidData)
		}
		parsed, err := parseUseDependencies(raw[open+1 : len(raw)-1])
		if err != nil {
			return Atom{}, err
		}
		result.UseFlags, raw = parsed, raw[:open]
	}
	if repoAt := strings.Index(raw, "::"); repoAt >= 0 {
		result.Repo, raw = raw[repoAt+2:], raw[:repoAt]
		if !atomRepoPattern.MatchString(result.Repo) || strings.Contains(raw, "::") {
			return Atom{}, fmt.Errorf("%w: repository in atom", ErrInvalidData)
		}
	}
	if slotAt := strings.IndexByte(raw, ':'); slotAt >= 0 {
		slot := raw[slotAt+1:]
		raw = raw[:slotAt]
		switch slot {
		case "=":
			result.SlotOp = SlotOpEq
		case "*":
			result.SlotOp = SlotOpStar
		default:
			if strings.HasSuffix(slot, "=") {
				result.SlotOp = SlotOpEq
				slot = strings.TrimSuffix(slot, "=")
			}
			if strings.Contains(slot, "*") {
				return Atom{}, fmt.Errorf("%w: invalid slot operator", ErrInvalidData)
			}
			result.Slot, result.Subslot, _ = strings.Cut(slot, "/")
			if !atomSlotPattern.MatchString(result.Slot) ||
				(result.Subslot != "" && !atomSlotPattern.MatchString(result.Subslot)) ||
				strings.Count(slot, "/") > 1 {
				return Atom{}, fmt.Errorf("%w: slot in atom", ErrInvalidData)
			}
		}
	}
	category, packageVersion, found := strings.Cut(raw, "/")
	if !found || category == "." || category == ".." || !atomCategoryPattern.MatchString(category) {
		return Atom{}, fmt.Errorf("%w: category in atom", ErrInvalidData)
	}
	result.Category = category
	result.Package = packageVersion
	for index := 0; index < len(packageVersion)-1; index++ {
		if packageVersion[index] != '-' || !asciiDigit(packageVersion[index+1]) {
			continue
		}
		versionRaw := packageVersion[index+1:]
		glob := strings.HasSuffix(versionRaw, "*")
		if glob {
			versionRaw = strings.TrimSuffix(versionRaw, "*")
		}
		version, err := ParseVersion(versionRaw)
		if err != nil {
			continue
		}
		result.Package, result.Version = packageVersion[:index], version
		if glob {
			if result.Op != OpEq {
				return Atom{}, fmt.Errorf("%w: version wildcard requires = operator", ErrInvalidData)
			}
			result.Op = OpEqGlob
		}
		break
	}
	if !atomPackagePattern.MatchString(result.Package) || strings.HasSuffix(result.Package, "-") {
		return Atom{}, fmt.Errorf("%w: package in atom", ErrInvalidData)
	}
	if result.Op != OpNone && result.Version == nil {
		return Atom{}, fmt.Errorf("%w: atom operator requires a version", ErrInvalidData)
	}
	if result.Version != nil && result.Op == OpNone && !allowBareVersion {
		return Atom{}, fmt.Errorf("%w: versioned package atom requires an operator", ErrInvalidData)
	}
	return result, nil
}

func parseUseDependencies(raw string) ([]UseFlag, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty USE dependency", ErrInvalidData)
	}
	seen := make(map[string]bool)
	result := make([]UseFlag, 0)
	for _, original := range strings.Split(raw, ",") {
		if original == "" {
			return nil, fmt.Errorf("%w: empty USE dependency", ErrInvalidData)
		}
		token := original
		item := UseFlag{Enabled: true}
		if strings.HasPrefix(token, "!") {
			item.Negated, token = true, token[1:]
		} else if strings.HasPrefix(token, "-") {
			item.Enabled, token = false, token[1:]
		}
		if strings.HasSuffix(token, "?") {
			item.Conditional, token = true, strings.TrimSuffix(token, "?")
		} else if strings.HasSuffix(token, "=") {
			item.Equal, token = true, strings.TrimSuffix(token, "=")
		}
		if strings.HasSuffix(token, "(+)") || strings.HasSuffix(token, "(-)") {
			value := strings.HasSuffix(token, "(+)")
			item.Default = &value
			token = token[:len(token)-3]
		}
		if token == "" || !validUseName(token) || seen[token] ||
			item.Negated && !item.Conditional && !item.Equal ||
			!item.Enabled && (item.Conditional || item.Equal) {
			return nil, fmt.Errorf("%w: USE dependency %q", ErrInvalidData, original)
		}
		seen[token], item.Name = true, token
		result = append(result, item)
	}
	return result, nil
}

func validUseName(value string) bool {
	for index := range len(value) {
		character := value[index]
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("+_-@", rune(character))) {
			return false
		}
	}
	return value != ""
}

// Compare returns -1, 0, or 1 using Gentoo package version ordering.
func (version *Version) Compare(other *Version) int {
	if version == nil && other == nil {
		return 0
	}
	if version == nil {
		return -1
	}
	if other == nil {
		return 1
	}
	left := parseVersionParts(*version)
	right := parseVersionParts(*other)
	maximum := len(left.numbers)
	if len(right.numbers) > maximum {
		maximum = len(right.numbers)
	}
	for index := 0; index < maximum; index++ {
		a, b := "0", "0"
		if index < len(left.numbers) {
			a = left.numbers[index]
		}
		if index < len(right.numbers) {
			b = right.numbers[index]
		}
		if index > 0 && (strings.HasPrefix(a, "0") || strings.HasPrefix(b, "0")) {
			width := len(a)
			if len(b) > width {
				width = len(b)
			}
			a, b = a+strings.Repeat("0", width-len(a)), b+strings.Repeat("0", width-len(b))
		}
		if compared := compareNumeric(a, b); compared != 0 {
			return compared
		}
	}
	if len(left.numbers) != len(right.numbers) {
		return compareInt(len(left.numbers), len(right.numbers))
	}
	if left.letter != right.letter {
		if left.letter < right.letter {
			return -1
		}
		return 1
	}
	maximum = len(left.suffixes)
	if len(right.suffixes) > maximum {
		maximum = len(right.suffixes)
	}
	for index := 0; index < maximum; index++ {
		a, b := versionSuffix{kind: "", number: "0"}, versionSuffix{kind: "", number: "0"}
		if index < len(left.suffixes) {
			a = left.suffixes[index]
		}
		if index < len(right.suffixes) {
			b = right.suffixes[index]
		}
		if suffixRank(a.kind) != suffixRank(b.kind) {
			return compareInt(suffixRank(a.kind), suffixRank(b.kind))
		}
		if compared := compareNumeric(a.number, b.number); compared != 0 {
			return compared
		}
	}
	return compareInt(version.Revision, other.Revision)
}

type versionSuffix struct{ kind, number string }
type versionParts struct {
	numbers  []string
	letter   string
	suffixes []versionSuffix
}

func parseVersionParts(version Version) versionParts {
	base := version.base
	if base == "" {
		parsed, _ := ParseVersion(version.Raw)
		base = parsed.base
	}
	match := versionSplitPattern.FindStringSubmatch(base)
	if len(match) == 0 {
		return versionParts{}
	}
	result := versionParts{numbers: strings.Split(match[1], "."), letter: match[2]}
	remaining := match[3]
	for remaining != "" {
		suffix := suffixPattern.FindStringSubmatch(remaining)
		if len(suffix) == 0 {
			break
		}
		number := suffix[2]
		if number == "" {
			number = "0"
		}
		result.suffixes = append(result.suffixes, versionSuffix{kind: suffix[1], number: number})
		remaining = suffix[3]
	}
	return result
}

func suffixRank(value string) int {
	switch value {
	case "alpha":
		return -4
	case "beta":
		return -3
	case "pre":
		return -2
	case "rc":
		return -1
	case "":
		return 0
	case "p":
		return 1
	default:
		return 2
	}
}

func compareNumeric(left, right string) int {
	a, b := new(big.Int), new(big.Int)
	a.SetString(left, 10)
	b.SetString(right, 10)
	return a.Cmp(b)
}

func compareInt(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

// Matches reports whether a package and its USE state satisfy the atom.
func (atom Atom) Matches(packageID PackageID, use UseState) (bool, error) {
	if atom.Category == "" || atom.Package == "" {
		return false, fmt.Errorf("%w: uninitialized atom", ErrInvalidData)
	}
	if atom.Category != packageID.Category || atom.Package != packageID.Name ||
		atom.Repo != "" && atom.Repo != packageID.Repository ||
		atom.Slot != "" && atom.Slot != packageID.Slot ||
		atom.Subslot != "" && atom.Subslot != packageID.Subslot {
		return false, nil
	}
	if atom.Version != nil {
		candidate, err := ParseVersion(packageID.Version)
		if err != nil {
			return false, err
		}
		comparison := candidate.Compare(atom.Version)
		matched := false
		switch atom.Op {
		case OpEq:
			matched = comparison == 0
		case OpLess:
			matched = comparison < 0
		case OpLessEq:
			matched = comparison <= 0
		case OpGt:
			matched = comparison > 0
		case OpGtEq:
			matched = comparison >= 0
		case OpTilde:
			matched = candidate.base == atom.Version.base
		case OpEqGlob:
			matched = strings.HasPrefix(candidate.Raw, atom.Version.Raw)
		default:
			return false, fmt.Errorf("%w: version without operator", ErrInvalidData)
		}
		if !matched {
			return false, nil
		}
	}
	for _, dependency := range atom.UseFlags {
		declared := use.Declared[dependency.Name]
		target := use.Enabled[dependency.Name]
		if !declared {
			if dependency.Default == nil {
				return false, nil
			}
			target = *dependency.Default
		}
		required := dependency.Enabled
		if dependency.Conditional || dependency.Equal {
			required = use.Caller[dependency.Name]
			if dependency.Negated {
				required = !required
			}
			if dependency.Conditional && !required {
				continue
			}
		}
		if target != required {
			return false, nil
		}
	}
	return true, nil
}

func (atom Atom) String() string {
	var result strings.Builder
	if atom.Op == OpEqGlob {
		result.WriteByte('=')
	} else {
		result.WriteString(string(atom.Op))
	}
	result.WriteString(atom.Category)
	result.WriteByte('/')
	result.WriteString(atom.Package)
	if atom.Version != nil {
		result.WriteByte('-')
		result.WriteString(atom.Version.Raw)
		if atom.Op == OpEqGlob {
			result.WriteByte('*')
		}
	}
	if atom.Slot != "" || atom.SlotOp != SlotOpNone {
		result.WriteByte(':')
		result.WriteString(atom.Slot)
		if atom.Subslot != "" {
			result.WriteByte('/')
			result.WriteString(atom.Subslot)
		}
		result.WriteString(string(atom.SlotOp))
	}
	if atom.Repo != "" {
		result.WriteString("::")
		result.WriteString(atom.Repo)
	}
	if len(atom.UseFlags) > 0 {
		result.WriteByte('[')
		for index, dependency := range atom.UseFlags {
			if index > 0 {
				result.WriteByte(',')
			}
			if dependency.Negated {
				result.WriteByte('!')
			} else if !dependency.Enabled {
				result.WriteByte('-')
			}
			result.WriteString(dependency.Name)
			if dependency.Default != nil {
				if *dependency.Default {
					result.WriteString("(+)")
				} else {
					result.WriteString("(-)")
				}
			}
			if dependency.Conditional {
				result.WriteByte('?')
			} else if dependency.Equal {
				result.WriteByte('=')
			}
		}
		result.WriteByte(']')
	}
	return result.String()
}
