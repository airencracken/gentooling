package gentooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// NewsPaths identifies one repository's GLEP 42 news and Portage state.
type NewsPaths struct {
	RepositoryName string
	NewsDirectory  string
	StateDirectory string
}

// NewsContext contains the explicit system state used for GLEP 42 relevance.
type NewsContext struct {
	Architecture      string
	Profile           string
	InstalledPackages []PackageID
	Language          string
}

// NewsItem is one parsed GLEP 42 repository news item.
type NewsItem struct {
	ID                 string
	Path               string
	Title              string
	Author             string
	Date               string
	Revision           int
	Format             string
	DisplayIfInstalled []string
	DisplayIfProfile   []string
	DisplayIfKeyword   []string
	Body               string
}

// NewsState separates repository contents, relevant items, and Portage's
// authoritative unread subset.
type NewsState struct {
	Items      []NewsItem
	Relevant   []NewsItem
	Unread     []NewsItem
	UnreadPath string
	SkipPath   string
}

// ReadNewsState reads GLEP 42 news without modifying Portage state. Repeated
// restrictions of the same kind are ORed and different kinds are ANDed.
func ReadNewsState(ctx context.Context, paths NewsPaths, newsContext NewsContext) (NewsState, error) {
	if err := ctx.Err(); err != nil {
		return NewsState{}, err
	}
	if paths.RepositoryName == "" || strings.ContainsAny(paths.RepositoryName, `/\\`) {
		return NewsState{}, fmt.Errorf("%w: invalid news repository name %q", ErrInvalidData, paths.RepositoryName)
	}
	if paths.NewsDirectory == "" || paths.StateDirectory == "" {
		return NewsState{}, fmt.Errorf("%w: news and state directories are required", ErrInvalidData)
	}
	language := newsContext.Language
	if language == "" {
		language = "en"
	}
	entries, err := os.ReadDir(paths.NewsDirectory)
	if os.IsNotExist(err) {
		entries = nil
	} else if err != nil {
		return NewsState{}, fmt.Errorf("read news directory %q: %w", paths.NewsDirectory, err)
	}
	state := NewsState{
		UnreadPath: filepath.Join(paths.StateDirectory, "news-"+paths.RepositoryName+".unread"),
		SkipPath:   filepath.Join(paths.StateDirectory, "news-"+paths.RepositoryName+".skip"),
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return NewsState{}, err
		}
		if !entry.IsDir() || len(entry.Name()) < 11 || entry.Name()[4] != '-' || entry.Name()[7] != '-' {
			continue
		}
		path := filepath.Join(paths.NewsDirectory, entry.Name(), entry.Name()+"."+language+".txt")
		item, err := readNewsItem(path, entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return NewsState{}, err
		}
		state.Items = append(state.Items, item)
		if newsRelevant(item, newsContext) {
			state.Relevant = append(state.Relevant, item)
		}
	}
	sort.Slice(state.Items, func(i, j int) bool { return state.Items[i].ID < state.Items[j].ID })
	sort.Slice(state.Relevant, func(i, j int) bool { return state.Relevant[i].ID < state.Relevant[j].ID })
	unread, found, err := readNewsNames(state.UnreadPath)
	if err != nil {
		return NewsState{}, err
	}
	if !found {
		for _, item := range state.Relevant {
			state.Unread = append(state.Unread, item)
		}
		return state, nil
	}
	// Portage's unread file is already the authoritative relevant set produced
	// by NewsManager. Do not re-evaluate it with potentially different caller
	// context, especially for read-only consumers that only need notifications.
	for _, item := range state.Items {
		if unread[item.ID] {
			state.Unread = append(state.Unread, item)
		}
	}
	return state, nil
}

func readNewsItem(path, id string) (NewsItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return NewsItem{}, err
	}
	item := NewsItem{ID: id, Path: path}
	parts := strings.SplitN(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n", 2)
	for _, line := range strings.Split(parts[0], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Title":
			item.Title = value
		case "Author":
			item.Author = value
		case "Date":
			item.Date = value
		case "Revision":
			item.Revision, _ = strconv.Atoi(value)
		case "News-Item-Format":
			item.Format = value
		case "Display-If-Installed":
			item.DisplayIfInstalled = append(item.DisplayIfInstalled, value)
		case "Display-If-Profile":
			item.DisplayIfProfile = append(item.DisplayIfProfile, value)
		case "Display-If-Keyword":
			item.DisplayIfKeyword = append(item.DisplayIfKeyword, value)
		}
	}
	if item.Format == "" || (!strings.HasPrefix(item.Format, "1.") && !strings.HasPrefix(item.Format, "2.")) {
		return NewsItem{}, fmt.Errorf("%w: news item %q has unsupported format %q", ErrInvalidData, id, item.Format)
	}
	if len(parts) == 2 {
		item.Body = strings.TrimSpace(parts[1])
	}
	return item, nil
}

func newsRelevant(item NewsItem, ctx NewsContext) bool {
	if !anyString(item.DisplayIfKeyword, func(value string) bool { return value == ctx.Architecture }) {
		return false
	}
	if !anyString(item.DisplayIfProfile, func(value string) bool {
		if strings.HasPrefix(item.Format, "2.") && strings.HasSuffix(value, "/*") {
			return strings.HasPrefix(ctx.Profile, strings.TrimSuffix(value, "*"))
		}
		return value == ctx.Profile
	}) {
		return false
	}
	return anyString(item.DisplayIfInstalled, func(value string) bool {
		atom, err := ParseAtom(value)
		if err != nil {
			return false
		}
		for _, installed := range ctx.InstalledPackages {
			if matched, err := atom.Matches(installed, UseState{}); err == nil && matched {
				return true
			}
		}
		return false
	})
}

func anyString(values []string, match func(string) bool) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if match(value) {
			return true
		}
	}
	return false
}

func readNewsNames(path string) (map[string]bool, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("read news state %q: %w", path, err)
	}
	names := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			names[line] = true
		}
	}
	return names, true, nil
}
