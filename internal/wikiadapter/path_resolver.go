package wikiadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathResolver struct {
	wikiRoot string
}

func NewPathResolver(wikiRoot string) *PathResolver {
	return &PathResolver{wikiRoot: filepath.Clean(wikiRoot)}
}

func (r *PathResolver) WikiRoot() string {
	return r.wikiRoot
}

func (r *PathResolver) ResolveWikiPath(rel string) (string, string, error) {
	clean, err := sanitizeRelPath(rel)
	if err != nil {
		return "", "", err
	}
	abs := filepath.Join(r.wikiRoot, filepath.FromSlash(clean))
	if !isWithin(abs, r.wikiRoot) {
		return "", "", fmt.Errorf("path escapes wiki root")
	}
	return abs, clean, nil
}

func (r *PathResolver) ResolveReadPath(rel string) (string, string, error) {
	abs, clean, err := r.ResolveWikiPath(rel)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", "", err
	}
	return abs, clean, nil
}

func (r *PathResolver) EnsureWritableWikiPath(rel string) (string, string, error) {
	abs, clean, err := r.ResolveWikiPath(rel)
	if err != nil {
		return "", "", err
	}
	if clean == "AGENT.md" || clean == "USER_GUIDE.md" {
		return "", "", fmt.Errorf("%s is read-only", clean)
	}
	if strings.HasPrefix(clean, "raw/") || clean == "raw" {
		return "", "", fmt.Errorf("raw is read-only")
	}
	if !strings.HasPrefix(clean, "wiki/") {
		return "", "", fmt.Errorf("writes are only allowed under wiki/")
	}
	return abs, clean, nil
}

func sanitizeRelPath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("path is required")
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("invalid path")
	}
	return clean, nil
}

func isWithin(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
