package main

import "strings"

// rule forbids packages in scope from importing a package that matches
// forbid, except for importers matching one of except. Patterns are paths
// relative to the module root: "a/b" matches that package only, "a/b/..."
// matches it and every package below it, and a "*" element matches any one
// path element. With ownTree set, an importer inside the same "*" subtree as
// the imported package is allowed, so a vendor adapter may import its own
// subpackages. An imported package matching one of neutral is not what
// forbid means, so every importer may use it.
type rule struct {
	id      string
	doc     string
	scope   []string
	forbid  string
	except  []string
	neutral []string
	ownTree bool
}

// rules is the one table of layering rules (D88). The baseline only shrinks,
// so a new rule must land with the code it flags already fixed.
var rules = []rule{
	{
		id:     "internal-imports-cmd",
		doc:    "no package under internal/ imports cmd/...",
		scope:  []string{"internal/..."},
		forbid: "cmd/...",
	},
	{
		id:     "core-imports-daemon",
		doc:    "only internal/daemon/... and cmd/... import internal/daemon",
		forbid: "internal/daemon",
		except: []string{"internal/daemon/...", "cmd/..."},
	},
	{
		id:      "imports-vendor-adapter",
		doc:     "vendor adapters internal/adapter/<vendor> are imported only by the daemon's wiring and their own <vendor> tree; others use internal/adapter interfaces and the vendor-neutral protocol packages under internal/adapter/protocol",
		forbid:  "internal/adapter/*/...",
		except:  []string{"internal/daemon/..."},
		neutral: []string{"internal/adapter/protocol/..."},
		ownTree: true,
	},
}

func (r *rule) violates(importer, imported string) bool {
	if len(r.scope) > 0 && !matchesAny(r.scope, importer) {
		return false
	}
	bound, ok := match(r.forbid, imported)
	if !ok || matchesAny(r.except, importer) || matchesAny(r.neutral, imported) {
		return false
	}
	if r.ownTree {
		if own, in := match(r.forbid, importer); in && own == bound {
			return false
		}
	}
	return true
}

func matchesAny(patterns []string, pkg string) bool {
	for _, p := range patterns {
		if _, ok := match(p, pkg); ok {
			return true
		}
	}
	return false
}

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
