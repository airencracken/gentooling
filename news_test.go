package gentooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeNewsFixture(t *testing.T, base, id, body string) {
	t.Helper()
	dir := filepath.Join(base, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".en.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadNewsStateAppliesGLEP42RelevanceAndPortageUnreadState(t *testing.T) {
	root := t.TempDir()
	newsDir, stateDir := filepath.Join(root, "news"), filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	common := "Author: dev@gentoo.org\nDate: 2026-01-01\nNews-Item-Format: 2.0\n"
	writeNewsFixture(t, newsDir, "2026-01-01-general", "Title: General\n"+common+"\nGeneral body.\n")
	writeNewsFixture(t, newsDir, "2026-01-02-relevant", "Title: Relevant\n"+common+"Display-If-Keyword: amd64\nDisplay-If-Profile: default/linux/amd64/23.0/*\nDisplay-If-Installed: >=app-editors/vim-9\n\nRelevant body.\n")
	writeNewsFixture(t, newsDir, "2026-01-03-other-arch", "Title: Other\n"+common+"Display-If-Keyword: arm64\n\nOther body.\n")
	if err := os.WriteFile(filepath.Join(stateDir, "news-gentoo.unread"), []byte("2026-01-02-relevant\n2026-01-03-other-arch\nmissing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := ReadNewsState(context.Background(), NewsPaths{RepositoryName: "gentoo", NewsDirectory: newsDir, StateDirectory: stateDir}, NewsContext{
		Architecture: "amd64", Profile: "default/linux/amd64/23.0/hardened", InstalledPackages: []PackageID{{Category: "app-editors", Name: "vim", Version: "9.1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 3 || len(state.Relevant) != 2 || len(state.Unread) != 2 || state.Unread[0].ID != "2026-01-02-relevant" || state.Unread[1].ID != "2026-01-03-other-arch" {
		t.Fatalf("news state = items %d relevant %d unread %#v", len(state.Items), len(state.Relevant), state.Unread)
	}
	if state.Relevant[1].Body != "Relevant body." {
		t.Fatalf("body = %q", state.Relevant[1].Body)
	}
}

func TestReadNewsStateORsLikeRestrictionsAndANDsKinds(t *testing.T) {
	root := t.TempDir()
	id := "2026-02-01-rules"
	writeNewsFixture(t, root, id, "Title: Rules\nNews-Item-Format: 2.0\nDisplay-If-Keyword: arm64\nDisplay-If-Keyword: amd64\nDisplay-If-Installed: app-misc/missing\nDisplay-If-Installed: app-misc/present\nDisplay-If-Profile: default/linux/*\n\nBody.\n")
	state, err := ReadNewsState(context.Background(), NewsPaths{RepositoryName: "test", NewsDirectory: root, StateDirectory: filepath.Join(root, "state")}, NewsContext{Architecture: "amd64", Profile: "default/linux/amd64/23.0", InstalledPackages: []PackageID{{Category: "app-misc", Name: "present", Version: "1"}}})
	if err != nil || len(state.Relevant) != 1 || len(state.Unread) != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestReadNewsStateRejectsInvalidInputsAndHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadNewsState(ctx, NewsPaths{}, NewsContext{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if _, err := ReadNewsState(context.Background(), NewsPaths{RepositoryName: "../bad", NewsDirectory: "/tmp", StateDirectory: "/tmp"}, NewsContext{}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("repository validation = %v", err)
	}
	root := t.TempDir()
	writeNewsFixture(t, root, "2026-01-01-invalid", "Title: Invalid\nNews-Item-Format: 9.0\n\nBody.\n")
	_, err := ReadNewsState(context.Background(), NewsPaths{RepositoryName: "test", NewsDirectory: root, StateDirectory: root}, NewsContext{})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("format validation = %v", err)
	}
}
