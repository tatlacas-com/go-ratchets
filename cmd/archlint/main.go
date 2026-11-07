// Command archlint is an import-direction ratchet (D88): it checks every
// direct import between packages of the module against the layering rules in
// rules.go, so logic cannot land in the wrong layer while meeting the size
// limits that internal/sizelint enforces.
//
// Imports that broke a rule before it existed are listed in a baseline as
// "<rule> <importer> <imported>". A violation not in the baseline fails, and
// so does a baseline entry that no longer occurs, until -tighten rewrites the
// baseline to the violations that remain. Nothing enters the baseline except
// by a reviewed edit.
//
// Production files only are checked: _test.go files may wire packages
// together across layers.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const defaultBaseline = ".ratchets/archlint-baseline.txt"

const baselineHeader = `# Imports that broke a layering rule before archlint (D88), as
# "<rule> <importer> <imported>" with module-relative package paths. This list
# may only shrink: never add an entry to make a new import pass. After removing
# an import run
#   go run ./internal/archlint -tighten
`

type violation struct {
	rule     string
	importer string
	imported string
}

func (v violation) String() string {
	return v.rule + " " + v.importer + " " + v.imported
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("archlint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "module root to scan")
	baselinePath := flags.String("baseline", "", "baseline file (default <root>/"+defaultBaseline+")")
	tighten := flags.Bool("tighten", false, "rewrite the baseline to the violations that remain")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(*root, defaultBaseline)
	}
	baseline, err := readBaseline(*baselinePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	edges, err := scan(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	found := check(edges)
	fresh, stale := compare(baseline, found)
	for _, v := range fresh {
		_, _ = fmt.Fprintf(stderr, "%s: %s imports %s, but %s; depend inward instead\n",
			v.rule, v.importer, v.imported, ruleDoc(v.rule))
	}
	if len(fresh) > 0 {
		if *tighten {
			_, _ = fmt.Fprintln(stderr, "archlint: refusing to tighten a baseline that would grow")
		}
		return 1
	}
	if len(stale) == 0 {
		return 0
	}
	if !*tighten {
		for _, v := range stale {
			_, _ = fmt.Fprintf(stderr, "%s: baselined but no longer found\n", v)
		}
		_, _ = fmt.Fprintln(stderr, "archlint: run `go run ./internal/archlint -tighten` to drop fixed violations from the baseline")
		return 1
	}
	if err := writeBaseline(*baselinePath, found); err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "archlint: baseline tightened (%d violations)\n", len(found))
	return 0
}

func check(edges map[edge]bool) []violation {
	var found []violation
	for e := range edges {
		for i := range rules {
			if r := &rules[i]; r.violates(e.importer, e.imported) {
				found = append(found, violation{rule: r.id, importer: e.importer, imported: e.imported})
			}
		}
	}
	slices.SortFunc(found, func(a, b violation) int { return strings.Compare(a.String(), b.String()) })
	return found
}

func compare(baseline map[violation]bool, found []violation) (fresh, stale []violation) {
	seen := map[violation]bool{}
	for _, v := range found {
		seen[v] = true
		if !baseline[v] {
			fresh = append(fresh, v)
		}
	}
	for v := range baseline {
		if !seen[v] {
			stale = append(stale, v)
		}
	}
	slices.SortFunc(stale, func(a, b violation) int { return strings.Compare(a.String(), b.String()) })
	return fresh, stale
}

func ruleDoc(id string) string {
	for i := range rules {
		if rules[i].id == id {
			return rules[i].doc
		}
	}
	return ""
}

func readBaseline(path string) (map[violation]bool, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	baseline := map[violation]bool{}
	lines := bufio.NewScanner(f)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || ruleDoc(fields[0]) == "" {
			return nil, fmt.Errorf("%s:%d: want \"<rule> <importer> <imported>\" with a known rule, got %q", path, n, line)
		}
		baseline[violation{rule: fields[0], importer: fields[1], imported: fields[2]}] = true
	}
	return baseline, lines.Err()
}

func writeBaseline(path string, found []violation) error {
	var b strings.Builder
	b.WriteString(baselineHeader)
	for _, v := range found {
		b.WriteString(v.String() + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
