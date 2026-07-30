package gentooling

import (
	"os"
	"strings"
	"testing"
)

func TestRepositoryDeclaresGPLVersion3License(t *testing.T) {
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		t.Fatalf("read LICENSE: %v", err)
	}
	text := string(license)
	for _, required := range []string{
		"GNU General Public License",
		"version 3",
		"WITHOUT ANY",
		"WARRANTY",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("LICENSE does not contain %q", required)
		}
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), "[LICENSE](LICENSE)") {
		t.Error("README does not link to LICENSE")
	}
}
