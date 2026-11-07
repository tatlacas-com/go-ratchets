package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatlacas-com/go-ratchets/internal/archlint"
	"github.com/tatlacas-com/go-ratchets/internal/config"
)

const (
	defaultBaseline = config.DefaultImportsBaseline
	baselineHeader  = archlint.BaselineHeader
)

var (
	run   = archlint.Run
	match = archlint.Match
)

// repoConfig holds the layering rules the fixtures are checked against.
const repoConfig = `rules:
  - name: internal-imports-cmd
    from: [internal/...]
    forbid: [cmd/...]
    why: no package under internal/ imports cmd/...
  - name: core-imports-daemon
    forbid: [internal/daemon]
    except: [internal/daemon/..., cmd/...]
    why: only internal/daemon/... and cmd/... import internal/daemon
  - name: imports-vendor-adapter
    forbid: [internal/adapter/*/...]
    except: [internal/daemon/...]
    neutral: [internal/adapter/protocol/...]
    own_tree: true
    why: vendor adapters are imported only by the daemon's wiring and their own tree
`

const fixtureModule = "example.com/fix"

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if files == nil {
		files = map[string]string{}
	}
	if _, own := files[config.FileName]; !own {
		files[config.FileName] = repoConfig
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixture(t *testing.T, baseline string, imports map[string][]string) string {
	t.Helper()
	files := map[string]string{
		"go.mod":        "module " + fixtureModule + "\n\ngo 1.26\n",
		defaultBaseline: baseline,
	}
	for file, pkgs := range imports {
		var b strings.Builder
		b.WriteString("package " + filepath.Base(filepath.Dir(file)) + "\n\nimport (\n")
		for _, p := range pkgs {
			b.WriteString("\t_ \"" + p + "\"\n")
		}
		b.WriteString(")\n")
		files[file] = b.String()
	}
	return writeTree(t, files)
}

func mod(rel string) string { return fixtureModule + "/" + rel }

func runIn(t *testing.T, root string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(append([]string{"-root", root}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestRun_AllowedImportsPass(t *testing.T) {
	root := fixture(t, "", map[string][]string{
		"cmd/app/main.go":                          {mod("internal/daemon"), "fmt"},
		"internal/daemon/d.go":                     {mod("internal/adapter/claude")},
		"internal/daemon/wire/w.go":                {mod("internal/daemon"), mod("internal/adapter/acp/registry")},
		"internal/adapter/acp/a.go":                {mod("internal/adapter/acp/registry"), mod("internal/adapter")},
		"internal/core/c.go":                       {mod("internal/adapter"), mod("internal/daemon/api")},
		"internal/core/c_test.go":                  {mod("cmd/app"), mod("internal/daemon")},
		"internal/core/testdata/x/x.go":            {mod("cmd/app")},
		"tools/nested/n.go":                        {mod("internal/daemon")},
		"vendor/v/v.go":                            {mod("internal/daemon")},
		"internal/.hidden/h/h.go":                  {mod("cmd/app")},
		"_skip/s/s.go":                             {mod("internal/daemon")},
		"internal/adapter/acp/registry/r.go":       nil,
		"internal/adapter/codex/x.go":              {mod("internal/adapter/protocol/acp/types")},
		"internal/core/p.go":                       {mod("internal/adapter/protocol/acp/types")},
		"internal/adapter/protocol/acp/types/t.go": nil,
	})
	if err := os.WriteFile(filepath.Join(root, "tools", "nested", "go.mod"), []byte("module example.com/nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runIn(t, root); code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
}

func TestRun_NewViolationNamesRuleImporterAndImported(t *testing.T) {
	root := fixture(t, "", map[string][]string{
		"internal/core/c.go":          {mod("cmd/app"), mod("internal/daemon")},
		"internal/adapter/codex/x.go": {mod("internal/adapter/acp/types")},
	})
	code, _, stderr := runIn(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{
		"internal-imports-cmd: internal/core imports cmd/app",
		"core-imports-daemon: internal/core imports internal/daemon",
		"imports-vendor-adapter: internal/adapter/codex imports internal/adapter/acp/types",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestRun_BaselinedViolationPasses(t *testing.T) {
	root := fixture(t, "# header\n\ncore-imports-daemon internal/tui internal/daemon\n", map[string][]string{
		"internal/tui/t.go": {mod("internal/daemon")},
	})
	if code, _, stderr := runIn(t, root); code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
}

func TestRun_StaleBaselineEntryFailsUntilTightened(t *testing.T) {
	baseline := "core-imports-daemon internal/tui internal/daemon\n" +
		"core-imports-daemon internal/client internal/daemon\n"
	root := fixture(t, baseline, map[string][]string{
		"internal/tui/t.go":    {mod("internal/daemon")},
		"internal/client/c.go": nil,
	})
	code, _, stderr := runIn(t, root)
	if code != 1 || !strings.Contains(stderr, "core-imports-daemon internal/client internal/daemon: baselined but no longer found") {
		t.Fatalf("exit %d, want 1 naming the stale entry; stderr:\n%s", code, stderr)
	}
	code, stdout, stderr := runIn(t, root, "-tighten")
	if code != 0 || !strings.Contains(stdout, "baseline tightened (1 violations)") {
		t.Fatalf("tighten exit %d; stdout %q stderr %q", code, stdout, stderr)
	}
	want := baselineHeader + "core-imports-daemon internal/tui internal/daemon\n"
	if got := readFile(t, filepath.Join(root, defaultBaseline)); got != want {
		t.Fatalf("baseline = %q, want %q", got, want)
	}
	if code, _, stderr := runIn(t, root); code != 0 {
		t.Fatalf("after tighten exit %d; stderr:\n%s", code, stderr)
	}
}

func TestRun_TightenRefusesToGrow(t *testing.T) {
	baseline := "core-imports-daemon internal/gone internal/daemon\n"
	root := fixture(t, baseline, map[string][]string{
		"internal/tui/t.go": {mod("internal/daemon")},
	})
	code, _, stderr := runIn(t, root, "-tighten")
	if code != 1 || !strings.Contains(stderr, "refusing to tighten") {
		t.Fatalf("exit %d, want 1 refusing; stderr:\n%s", code, stderr)
	}
	if got := readFile(t, filepath.Join(root, defaultBaseline)); got != baseline {
		t.Fatalf("baseline changed to %q", got)
	}
}

func TestRun_SetupErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		args  []string
		want  string
	}{
		"bad flag":        {args: []string{"-nope"}, want: "-nope"},
		"no baseline":     {files: map[string]string{"go.mod": "module m\n"}, want: "baseline.txt"},
		"bad entry":       {files: map[string]string{"go.mod": "module m\n", defaultBaseline: "a b\n"}, want: "want \"<rule> <importer> <imported>\""},
		"unknown rule":    {files: map[string]string{"go.mod": "module m\n", defaultBaseline: "nope a b\n"}, want: "known rule"},
		"no go.mod":       {files: map[string]string{defaultBaseline: ""}, want: "go.mod"},
		"no module line":  {files: map[string]string{"go.mod": "go 1.26\n", defaultBaseline: ""}, want: "no module directive"},
		"bad module path": {files: map[string]string{"go.mod": "module \"bad path\"\n", defaultBaseline: ""}, want: "bad path"},
		"unparsable mod":  {files: map[string]string{"go.mod": "module (\n", defaultBaseline: ""}, want: "go.mod"},
		"unparsable go":   {files: map[string]string{"go.mod": "module m\n", defaultBaseline: "", "p/p.go": "package"}, want: "p.go"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			code, _, stderr := runIn(t, root, tc.args...)
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, want 2 with %q; stderr:\n%s", code, tc.want, stderr)
			}
		})
	}
}

func TestRun_ModuleDirectiveSyntax(t *testing.T) {
	for name, directive := range map[string]string{
		"trailing comment": "module " + fixtureModule + " // comment",
		"quoted":           "module \"" + fixtureModule + "\"",
		"whitespace":       "  module\t " + fixtureModule + " \t",
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t, "", map[string][]string{"internal/core/c.go": {mod("internal/daemon")}})
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(directive+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runIn(t, root)
			if code != 1 || !strings.Contains(stderr, "core-imports-daemon: internal/core imports internal/daemon") {
				t.Fatalf("exit %d, want 1 naming the violation; stderr:\n%s", code, stderr)
			}
		})
	}
}

func TestRun_SymlinkRootIsScanned(t *testing.T) {
	baseline := "core-imports-daemon internal/tui internal/daemon\n"
	root := fixture(t, baseline, map[string][]string{
		"internal/tui/t.go":  {mod("internal/daemon")},
		"internal/core/c.go": {mod("cmd/app")},
	})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runIn(t, link, "-tighten")
	if code != 1 || !strings.Contains(stderr, "internal-imports-cmd: internal/core imports cmd/app") {
		t.Fatalf("exit %d, want 1 naming the violation; stderr:\n%s", code, stderr)
	}
	if got := readFile(t, filepath.Join(root, defaultBaseline)); got != baseline {
		t.Fatalf("baseline changed to %q", got)
	}
}

func TestRun_TightenWriteFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	root := fixture(t, "", map[string][]string{"internal/tui/t.go": nil})
	baseline := filepath.Join(root, "readonly-baseline.txt")
	if err := os.WriteFile(baseline, []byte("core-imports-daemon internal/tui internal/daemon\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runIn(t, root, "-baseline", baseline, "-tighten"); code != 2 {
		t.Fatalf("exit %d, want 2; stderr:\n%s", code, stderr)
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, pkg, bound string
		ok                  bool
	}{
		{"internal/daemon", "internal/daemon", "", true},
		{"internal/daemon", "internal/daemon/api", "", false},
		{"internal/daemon/...", "internal/daemon", "", true},
		{"internal/daemon/...", "internal/daemonx", "", false},
		{"internal/adapter/*/...", "internal/adapter", "", false},
		{"internal/adapter/*/...", "internal/adapter/acp/registry", "acp", true},
		{"internal/adapter/*", "internal/adapter/acp/registry", "", false},
	} {
		bound, ok := match(tc.pattern, tc.pkg)
		if bound != tc.bound || ok != tc.ok {
			t.Errorf("match(%q, %q) = %q, %v; want %q, %v", tc.pattern, tc.pkg, bound, ok, tc.bound, tc.ok)
		}
	}
}

func TestRepoPassesItsBaseline(t *testing.T) {
	if code, _, stderr := runIn(t, filepath.Join("..", "..")); code != 0 {
		t.Fatalf("archlint on the repo exit %d; stderr:\n%s", code, stderr)
	}
}
