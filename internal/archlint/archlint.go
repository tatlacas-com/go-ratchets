// Package archlint is an import-direction ratchet: it checks every direct
// import of a package of the module, to another package of it or to a
// third-party package, against the rules in the repo's .ratchets.yml, so
// logic cannot land in the wrong layer while meeting the size limits that
// sizelint enforces.
//
// Imports that broke a rule before it existed are listed in a baseline as
// "<rule> <importer> <imported>". A violation not in the baseline fails, and
// so does a baseline entry that no longer occurs, until -tighten rewrites the
// baseline to the violations that remain. Nothing enters the baseline except
// by a reviewed edit.
//
// Production files only are checked: _test.go files may wire packages
// together across layers.
package archlint

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tatlacas-com/go-ratchets/internal/config"
)

// BaselineHeader opens every baseline -tighten writes.
const BaselineHeader = `# Imports that broke a layering rule before archlint, as
# "<rule> <importer> <imported>" with module-relative package paths (full import
# paths for third-party packages). This list may only shrink: never add an entry
# to make a new import pass. After removing an import run
#   archlint -tighten
`

type violation struct {
	rule     string
	importer string
	imported string
}

func (v violation) String() string {
	return v.rule + " " + v.importer + " " + v.imported
}

// Run is the archlint command: args are its flags, and the result is its exit
// status.
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("archlint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "module root to scan; its "+config.FileName+" holds the rules")
	baselinePath := flags.String("baseline", "", "baseline file (default from the config, "+config.DefaultImportsBaseline+")")
	tighten := flags.Bool("tighten", false, "rewrite the baseline to the violations that remain")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	if *baselinePath == "" {
		*baselinePath = config.Resolve(*root, cfg.Baselines.Imports)
	}
	found, fresh, stale, err := evaluate(*root, &cfg, *baselinePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	for _, v := range fresh {
		_, _ = fmt.Fprintln(stderr, freshMessage(&cfg, v))
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
			_, _ = fmt.Fprintln(stderr, staleMessage(v))
		}
		_, _ = fmt.Fprintln(stderr, "archlint: run `archlint -tighten` to drop fixed violations from the baseline")
		return 1
	}
	if err := writeBaseline(*baselinePath, found); err != nil {
		_, _ = fmt.Fprintln(stderr, "archlint:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "archlint: baseline tightened (%d violations)\n", len(found))
	return 0
}

// Check returns a problem for every violation not in the baseline and for
// every baseline entry that no longer occurs.
func Check(root string, cfg *config.Config) ([]string, error) {
	_, fresh, stale, err := evaluate(root, cfg, config.Resolve(root, cfg.Baselines.Imports))
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, v := range fresh {
		problems = append(problems, freshMessage(cfg, v))
	}
	for _, v := range stale {
		problems = append(problems, staleMessage(v))
	}
	return problems, nil
}

func evaluate(root string, cfg *config.Config, baselinePath string) (found, fresh, stale []violation, err error) {
	baseline, err := readBaseline(baselinePath, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	edges, err := scan(root)
	if err != nil {
		return nil, nil, nil, err
	}
	found = check(cfg, edges)
	fresh, stale = compare(baseline, found)
	return found, fresh, stale, nil
}

func freshMessage(cfg *config.Config, v violation) string {
	why := ""
	if r, ok := cfg.Rule(v.rule); ok {
		why = r.Why
	}
	return fmt.Sprintf("%s: %s imports %s, but %s; depend inward instead", v.rule, v.importer, v.imported, why)
}

func staleMessage(v violation) string { return v.String() + ": baselined but no longer found" }

func check(cfg *config.Config, edges map[edge]bool) []violation {
	var found []violation
	for e := range edges {
		for i := range cfg.Rules {
			if r := &cfg.Rules[i]; violates(r, e.importer, e.imported) {
				found = append(found, violation{rule: r.Name, importer: e.importer, imported: e.imported})
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

func readBaseline(path string, cfg *config.Config) (map[violation]bool, error) {
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
		if _, known := cfg.Rule(fields[0]); len(fields) != 3 || !known {
			return nil, fmt.Errorf("%s:%d: want \"<rule> <importer> <imported>\" with a known rule, got %q", path, n, line)
		}
		baseline[violation{rule: fields[0], importer: fields[1], imported: fields[2]}] = true
	}
	return baseline, lines.Err()
}

func writeBaseline(path string, found []violation) error {
	var b strings.Builder
	b.WriteString(BaselineHeader)
	for _, v := range found {
		b.WriteString(v.String() + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
