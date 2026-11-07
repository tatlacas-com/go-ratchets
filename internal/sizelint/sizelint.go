// Package sizelint is a size ratchet: a production Go file may not exceed the
// configured file limit (default 800 lines), a Go function or method body the
// func limit (80), nor a shell script under a configured script dir the script
// limit (100).
//
// Items over a limit that predate the rule are listed in a baseline with
// their size. A baselined item may shrink but never grow past its recorded
// size, and -tighten rewrites the baseline to current sizes, dropping items
// back under their limit and items that no longer exist. Nothing enters the
// baseline except by a reviewed edit, so a renamed or new oversized item fails
// until it is split.
//
// A function is keyed by its name (with its receiver type for a method), a
// package-level function literal by the var it is assigned to, and the nth
// function sharing a key in a file by "<name>#n".
//
// Test files, files with a "Code generated ... DO NOT EDIT." header, and, as
// for the go tool, testdata, vendor and directories starting with "." or "_"
// are not scanned for Go. Scripts (*.sh) are scanned under the config's
// script_dirs.
package sizelint

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tatlacas-com/go-ratchets/internal/config"
)

// BaselineHeader opens every baseline -tighten writes.
const BaselineHeader = `# Files, functions and scripts over their size limit that predate sizelint,
# as "<kind> <path> <func or -> <lines>". This list may only shrink: split an
# item instead of growing it, and after shrinking items run
#   sizelint -tighten
`

type kind string

const (
	goFile   kind = "file"
	goFunc   kind = "func"
	shScript kind = "script"
)

func limitOf(l config.Limits, k kind) int {
	switch k {
	case goFile:
		return l.File
	case goFunc:
		return l.Func
	default:
		return l.Script
	}
}

type item struct {
	kind kind
	path string
	fn   string
}

func (i item) String() string {
	if i.kind == goFunc {
		return fmt.Sprintf("%s %s %s", i.kind, i.path, i.fn)
	}
	return fmt.Sprintf("%s %s", i.kind, i.path)
}

func (i item) baselineKey() string {
	fn := i.fn
	if fn == "" {
		fn = "-"
	}
	return fmt.Sprintf("%s %s %s", i.kind, i.path, fn)
}

// Run is the sizelint command: args are its flags, and the result is its exit
// status.
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sizelint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repo root to scan; its "+config.FileName+" configures the limits")
	baselinePath := flags.String("baseline", "", "baseline file (default from the config, "+config.DefaultSizeBaseline+")")
	tighten := flags.Bool("tighten", false, "rewrite the baseline to current sizes, dropping items under their limit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sizelint:", err)
		return 2
	}
	if *baselinePath == "" {
		if *baselinePath, err = config.Resolve(*root, cfg.Baselines.Size); err != nil {
			_, _ = fmt.Fprintln(stderr, "sizelint:", err)
			return 2
		}
	}
	baseline, found, err := load(*root, &cfg, *baselinePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sizelint:", err)
		return 2
	}
	problems, stale, tightened := compare(cfg.Limits, baseline, found)
	for _, problem := range problems {
		_, _ = fmt.Fprintln(stderr, problem)
	}
	if len(problems) > 0 {
		if *tighten {
			_, _ = fmt.Fprintln(stderr, "sizelint: refusing to tighten a baseline that would grow")
		}
		return 1
	}
	if len(stale) > 0 && !*tighten {
		for _, problem := range stale {
			_, _ = fmt.Fprintln(stderr, problem)
		}
		_, _ = fmt.Fprintln(stderr, "sizelint: run `sizelint -tighten` to drop vanished items from the baseline")
		return 1
	}
	if !*tighten {
		if stale := len(baseline) - countSame(baseline, tightened); stale > 0 {
			_, _ = fmt.Fprintf(stdout, "sizelint: %d baselined items shrank or went; run `sizelint -tighten` to lock that in\n", stale)
		}
		return 0
	}
	if err := writeBaseline(*baselinePath, tightened); err != nil {
		_, _ = fmt.Fprintln(stderr, "sizelint:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "sizelint: baseline tightened (%d items)\n", len(tightened))
	return 0
}

// Check returns a problem for every item over its limit that is not
// baselined or has grown past its baseline, and for every baseline entry whose
// file, function or script no longer exists. Baselined items that merely
// shrank are not problems; Run only hints at -tighten for them.
func Check(root string, cfg *config.Config) ([]string, error) {
	path, err := config.Resolve(root, cfg.Baselines.Size)
	if err != nil {
		return nil, err
	}
	baseline, found, err := load(root, cfg, path)
	if err != nil {
		return nil, err
	}
	problems, stale, _ := compare(cfg.Limits, baseline, found)
	return append(problems, stale...), nil
}

func load(root string, cfg *config.Config, baselinePath string) (baseline, found map[item]int, err error) {
	if baseline, err = readBaseline(baselinePath); err != nil {
		return nil, nil, err
	}
	if found, err = scan(root, cfg.ScriptDirs); err != nil {
		return nil, nil, err
	}
	return baseline, found, nil
}

// compare returns a problem per item over its limit and its baseline, a stale
// entry per baseline item that no longer exists, and the baseline tightened to
// the current size of each item still over its limit.
func compare(lim config.Limits, baseline, found map[item]int) (problems, stale []string, tightened map[item]int) {
	tightened = map[item]int{}
	for _, it := range sortedItems(baseline) {
		if _, exists := found[it]; !exists {
			stale = append(stale, fmt.Sprintf("%s: baselined but no longer found", it.baselineKey()))
		}
	}
	for _, it := range sortedItems(found) {
		size, limit := found[it], limitOf(lim, it.kind)
		if size <= limit {
			continue
		}
		allowed, listed := baseline[it]
		switch {
		case !listed:
			problems = append(problems, fmt.Sprintf(
				"%s: %d lines exceeds the %d-line limit and is not baselined; split it",
				it, size, limit))
		case size > allowed:
			problems = append(problems, fmt.Sprintf(
				"%s: %d lines grew past its baseline of %d (limit %d); split it",
				it, size, allowed, limit))
		default:
			tightened[it] = size
		}
	}
	return problems, stale, tightened
}

func countSame(baseline, tightened map[item]int) int {
	same := 0
	for it, size := range tightened {
		if baseline[it] == size {
			same++
		}
	}
	return same
}

func sortedItems(m map[item]int) []item {
	items := make([]item, 0, len(m))
	for it := range m {
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, b item) int { return strings.Compare(a.baselineKey(), b.baselineKey()) })
	return items
}

func readBaseline(path string) (map[item]int, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	baseline := map[item]int{}
	lines := bufio.NewScanner(f)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		it, size, ok := parseBaselineLine(line)
		if !ok {
			return nil, fmt.Errorf("%s:%d: want \"<file|func|script> <path> <func or -> <positive lines>\", got %q", path, n, line)
		}
		baseline[it] = size
	}
	return baseline, lines.Err()
}

func parseBaselineLine(line string) (item, int, bool) {
	fields := strings.Fields(line)
	if len(fields) != 4 {
		return item{}, 0, false
	}
	size, err := strconv.Atoi(fields[3])
	if err != nil || size <= 0 {
		return item{}, 0, false
	}
	it := item{kind: kind(fields[0]), path: fields[1]}
	if it.kind != goFile && it.kind != goFunc && it.kind != shScript {
		return item{}, 0, false
	}
	if it.kind == goFunc {
		it.fn = fields[2]
	} else if fields[2] != "-" {
		return item{}, 0, false
	}
	return it, size, true
}

func writeBaseline(path string, items map[item]int) error {
	var b strings.Builder
	b.WriteString(BaselineHeader)
	for _, it := range sortedItems(items) {
		fmt.Fprintf(&b, "%s %d\n", it.baselineKey(), items[it])
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// scan sizes every production Go file and function and every shell script
// under the script dirs, keyed by slash-separated paths relative to root.
func scan(root string, scriptDirs []string) (map[item]int, error) {
	found := map[item]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipGoDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return sizeGoFile(root, path, found)
	})
	if err != nil {
		return nil, err
	}
	if err := sizeScripts(root, scriptDirs, found); err != nil {
		return nil, err
	}
	return found, nil
}

func skipGoDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func sizeGoFile(root, path string, found map[item]int) error {
	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	rel := relSlash(root, path)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	if ast.IsGenerated(f) {
		return nil
	}
	found[item{kind: goFile, path: rel}] = CountLines(src)
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				addFunc(found, rel, funcName(d), bodyLines(fset, d.Body))
			}
		case *ast.GenDecl:
			sizeFuncLits(fset, rel, d, found)
		}
	}
	return nil
}

// sizeFuncLits sizes each outermost function literal in a package-level
// var or const under the name it is assigned to; literals nested in it count
// toward its body, as literals in a FuncDecl count toward the FuncDecl.
func sizeFuncLits(fset *token.FileSet, rel string, d *ast.GenDecl, found map[item]int) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, value := range vs.Values {
			name := vs.Names[min(i, len(vs.Names)-1)].Name
			ast.Inspect(value, func(n ast.Node) bool {
				lit, isLit := n.(*ast.FuncLit)
				if isLit {
					addFunc(found, rel, name, bodyLines(fset, lit.Body))
				}
				return !isLit
			})
		}
	}
}

// addFunc keys the nth function sharing a name in a file, such as a second
// init or a second literal in one var, as "<name>#n", so each is held to the
// limit on its own rather than hiding behind a baselined namesake.
func addFunc(found map[item]int, path, name string, size int) {
	it := item{kind: goFunc, path: path, fn: name}
	for n := 2; ; n++ {
		if _, taken := found[it]; !taken {
			break
		}
		it.fn = name + "#" + strconv.Itoa(n)
	}
	found[it] = size
}

// bodyLines counts the lines a body spans from its opening to its closing
// brace, so a multi-line signature does not count against the limit.
func bodyLines(fset *token.FileSet, body *ast.BlockStmt) int {
	return fset.Position(body.Rbrace).Line - fset.Position(body.Lbrace).Line + 1
}

// funcName ignores receiver type parameters and pointers so the key survives
// either being edited.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recv := fd.Recv.List[0].Type
	for {
		switch r := recv.(type) {
		case *ast.StarExpr:
			recv = r.X
		case *ast.IndexExpr:
			recv = r.X
		case *ast.IndexListExpr:
			recv = r.X
		case *ast.Ident:
			return r.Name + "." + fd.Name.Name
		default:
			return fd.Name.Name
		}
	}
}

func sizeScripts(root string, scriptDirs []string, found map[item]int) error {
	for _, dir := range scriptDirs {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sh") {
				return err
			}
			return sizeScript(root, path, found)
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func sizeScript(root, path string, found map[item]int) error {
	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	found[item{kind: shScript, path: relSlash(root, path)}] = CountLines(src)
	return nil
}

func relSlash(root, path string) string {
	rel, _ := filepath.Rel(root, path) // path is under root, so Rel cannot fail
	return filepath.ToSlash(rel)
}

// CountLines counts lines as wc -l does, plus an unterminated last line.
func CountLines(src []byte) int {
	n := bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	return n
}
