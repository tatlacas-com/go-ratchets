package archlint

import (
	"strings"

	"github.com/tatlacas-com/go-ratchets/internal/config"
)

// violates reports whether importer importing imported breaks r. Importer
// is a module-relative package path; imported is either one, or, for a
// third-party package, its full import path.
func violates(r *config.Rule, importer, imported string) bool {
	if len(r.From) > 0 && !matchesAny(r.From, importer) {
		return false
	}
	pat, ok := forbids(r.Forbid, imported)
	if !ok || matchesAny(r.Except, importer) || matchesAny(r.Neutral, imported) {
		return false
	}
	if r.OwnTree && pat != nil {
		bound, _ := match(*pat, imported)
		if own, in := match(*pat, importer); in && own == bound {
			return false
		}
	}
	return true
}

// isThirdParty reports whether a forbid pattern names an import-path prefix
// rather than a module-local package: its first element contains a dot.
func isThirdParty(pattern string) bool {
	first, _, _ := strings.Cut(pattern, "/")
	return strings.Contains(first, ".")
}

// forbids reports whether imported matches any forbid pattern, returning the
// first module-local pattern that matched.
func forbids(patterns []string, imported string) (matched *string, ok bool) {
	for i, p := range patterns {
		if isThirdParty(p) {
			if hasPathPrefix(imported, p) {
				return nil, true
			}
			continue
		}
		if _, hit := match(p, imported); hit {
			return &patterns[i], true
		}
	}
	return nil, false
}

// hasPathPrefix reports whether pkg is prefix or lies below it, whole path
// elements only: "github.com/a/b" covers "github.com/a/b/c", not "…/bc".
func hasPathPrefix(pkg, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

func matchesAny(patterns []string, pkg string) bool {
	for _, p := range patterns {
		if _, ok := match(p, pkg); ok {
			return true
		}
	}
	return false
}

// Match reports whether pkg matches pattern, and what the "*" elements of
// pattern matched in it, joined by "/".
func Match(pattern, pkg string) (bound string, ok bool) { return match(pattern, pkg) }

func match(pattern, pkg string) (bound string, ok bool) {
	pat := strings.Split(pattern, "/")
	tree := pat[len(pat)-1] == "..."
	if tree {
		pat = pat[:len(pat)-1]
	}
	elems := strings.Split(pkg, "/")
	if len(elems) < len(pat) || (!tree && len(elems) != len(pat)) {
		return "", false
	}
	var stars []string
	for i, p := range pat {
		switch p {
		case "*":
			stars = append(stars, elems[i])
		case elems[i]:
		default:
			return "", false
		}
	}
	return strings.Join(stars, "/"), true
}
