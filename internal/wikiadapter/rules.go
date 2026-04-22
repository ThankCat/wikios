package wikiadapter

import "regexp"

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func IsValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

func IsSystemPage(relPath string) bool {
	switch relPath {
	case "wiki/index.md", "wiki/log.md", "wiki/overview.md", "wiki/QUESTIONS.md":
		return true
	default:
		return false
	}
}

func NeedsGraphExcluded(relPath string) bool {
	if IsSystemPage(relPath) {
		return true
	}
	return len(relPath) >= len("wiki/outputs/") && relPath[:len("wiki/outputs/")] == "wiki/outputs/"
}
