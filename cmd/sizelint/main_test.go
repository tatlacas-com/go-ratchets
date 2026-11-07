package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatlacas-com/go-ratchets/internal/config"
	"github.com/tatlacas-com/go-ratchets/internal/sizelint"
)

const (
	defaultBaseline = config.DefaultSizeBaseline
	baselineHeader  = sizelint.BaselineHeader
)

var (
	run        = sizelint.Run
	countLines = sizelint.CountLines
)

// repoConfig is the .ratchets.yml the fixtures share: the script dirs the
// tests below put scripts under.
const repoConfig = "script_dirs: [.fompiler, internal/daemon/bundle/teammates]\n"

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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

func runIn(t *testing.T, root string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(append([]string{"-root", root}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// goSource is a file of exactly lines lines whose function fn has a body
// spanning bodyLines lines, braces included.
func goSource(lines int, fn string, bodyLines int) string {
	var b strings.Builder
	b.WriteString("package p\n\nfunc " + fn + "() {\n")
	for range bodyLines - 2 {
		b.WriteString("\t_ = 0\n")
	}
	b.WriteString("}\n")
	for n := 2 + bodyLines; n < lines; n++ {
		b.WriteString("\n")
	}
	return b.String()
}

func script(lines int) string {
	return strings.Repeat("echo\n", lines)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestRun_UnderLimitsPasses(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline:       "",
		"p/p.go":              goSource(800, "F", 80),
		".fompiler/pipe/x.sh": script(100),
		".fompiler/notes.md":  script(500),
		"internal/daemon/bundle/teammates/t/y.sh": script(100),
		"p/big_test.go":            goSource(2000, "G", 500),
		"internal/x/testdata/t.go": goSource(2000, "G", 500),
	})
	if code, _, stderr := runIn(t, root); code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
}

func TestRun_NewOverLimitItemsFail(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline:       "",
		"p/p.go":              goSource(801, "F", 81),
		".fompiler/pipe/x.sh": script(101),
		"internal/daemon/bundle/teammates/t/y.sh": script(101),
	})
	code, _, stderr := runIn(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{
		"file p/p.go: 801 lines exceeds the 800-line limit and is not baselined; split it",
		"func p/p.go F: 81 lines exceeds the 80-line limit and is not baselined; split it",
		"script .fompiler/pipe/x.sh: 101 lines exceeds the 100-line limit and is not baselined; split it",
		"script internal/daemon/bundle/teammates/t/y.sh: 101 lines exceeds the 100-line limit and is not baselined; split it",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestRun_BaselinedAtSizePasses(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline:  "# comment\n\nfile p/p.go - 900\nfunc p/p.go T.M 90\nscript .fompiler/x.sh - 120\n",
		"p/p.go":         strings.Replace(goSource(900, "M", 90), "func M()", "func (t *T[K]) M()", 1),
		".fompiler/x.sh": script(120),
	})
	code, stdout, stderr := runIn(t, root)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing when the baseline is tight", stdout)
	}
}

func TestRun_BaselinedGrownFails(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline:  "file p/p.go - 900\nfunc p/p.go F 90\nscript .fompiler/x.sh - 120\n",
		"p/p.go":         goSource(901, "F", 91),
		".fompiler/x.sh": script(121),
	})
	code, _, stderr := runIn(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{
		"file p/p.go: 901 lines grew past its baseline of 900 (limit 800); split it",
		"func p/p.go F: 91 lines grew past its baseline of 90 (limit 80); split it",
		"script .fompiler/x.sh: 121 lines grew past its baseline of 120 (limit 100); split it",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestRun_ShrunkPassesAndTightenShrinksAndDrops(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline:  "file p/p.go - 900\nfunc p/p.go F 90\nfunc p/gone.go G 90\nscript .fompiler/x.sh - 120\n",
		"p/p.go":         goSource(850, "F", 80),
		".fompiler/x.sh": script(100),
	})
	code, stdout, stderr := runIn(t, root)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "4 baselined items shrank or went") {
		t.Errorf("stdout lacks the tighten hint:\n%s", stdout)
	}
	if code, _, stderr := runIn(t, root, "-tighten"); code != 0 {
		t.Fatalf("tighten exit %d; stderr:\n%s", code, stderr)
	}
	want := baselineHeader + "file p/p.go - 850\n"
	if got := readFile(t, filepath.Join(root, defaultBaseline)); got != want {
		t.Errorf("baseline =\n%s\nwant\n%s", got, want)
	}
	if code, stdout, stderr := runIn(t, root); code != 0 || stdout != "" {
		t.Fatalf("after tighten: exit %d, stdout %q; stderr:\n%s", code, stdout, stderr)
	}
}

func TestRun_TightenRefusesNewItems(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline: "",
		"p/p.go":        goSource(801, "F", 2),
	})
	code, _, stderr := runIn(t, root, "-tighten")
	if code != 1 || !strings.Contains(stderr, "refusing to tighten") {
		t.Fatalf("exit %d, want 1 refusing to tighten; stderr:\n%s", code, stderr)
	}
	if got := readFile(t, filepath.Join(root, defaultBaseline)); got != "" {
		t.Errorf("baseline rewritten to %q", got)
	}
}

func TestRun_GeneratedFileIgnored(t *testing.T) {
	root := writeTree(t, map[string]string{
		defaultBaseline: "",
		"p/gen.go":      "// Code generated by tool; DO NOT EDIT.\n\n" + goSource(2000, "F", 500),
	})
	if code, _, stderr := runIn(t, root); code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, stderr)
	}
}

func TestRun_DuplicateFuncNamesAreKeyedByOrdinal(t *testing.T) {
	src := goSource(0, "init", 85) + "\nfunc init() {\n}\n" + strings.Replace(goSource(0, "init", 81), "package p\n", "", 1)
	root := writeTree(t, map[string]string{
		defaultBaseline: "func p/p.go init 85\n",
		"p/p.go":        src,
	})
	code, _, stderr := runIn(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	want := "func p/p.go init#3: 81 lines exceeds the 80-line limit and is not baselined; split it"
	if !strings.Contains(stderr, want) || strings.Count(stderr, "\n") != 1 {
		t.Errorf("stderr = %q, want only %q", stderr, want)
	}
}

func TestRun_PackageLevelFuncLitsAreSized(t *testing.T) {
	body := func(lines int) string {
		return "func() {\n" + strings.Repeat("\t_ = func() {}\n", lines-2) + "}"
	}
	src := "package p\n\nvar handle = " + body(81) + "\n\n" +
		"var handlers = map[string]func(){\n\"a\": " + body(10) + ",\n\"b\": " + body(82) + ",\n}\n\n" +
		"var x, y = 1, " + body(83) + "\n\nconst c = 1\n\ntype T int\n"
	root := writeTree(t, map[string]string{
		defaultBaseline: "",
		"p/p.go":        src,
	})
	code, _, stderr := runIn(t, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{
		"func p/p.go handle: 81 lines",
		"func p/p.go handlers#2: 82 lines",
		"func p/p.go y: 83 lines",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if n := strings.Count(stderr, "\n"); n != 3 {
		t.Errorf("stderr has %d problems, want 3:\n%s", n, stderr)
	}
}

func TestRun_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		args  []string
		want  string
	}{
		"bad flag":          {args: []string{"-nope"}, want: "flag provided but not defined"},
		"missing baseline":  {want: "no such file"},
		"wrong field count": {files: map[string]string{defaultBaseline: "file p.go 900\n"}, want: "baseline.txt:1:"},
		"bad size":          {files: map[string]string{defaultBaseline: "file p.go - 0\n"}, want: "baseline.txt:1:"},
		"unknown kind":      {files: map[string]string{defaultBaseline: "dir p - 900\n"}, want: "baseline.txt:1:"},
		"file with func":    {files: map[string]string{defaultBaseline: "file p.go F 900\n"}, want: "baseline.txt:1:"},
		"unparsable go":     {files: map[string]string{defaultBaseline: "", "p/p.go": "package\n"}, want: "p/p.go:"},
	} {
		t.Run(name, func(t *testing.T) {
			files := tc.files
			if files == nil {
				files = map[string]string{"README": ""}
			}
			code, _, stderr := runIn(t, writeTree(t, files), tc.args...)
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, want 2 with %q; stderr:\n%s", code, tc.want, stderr)
			}
		})
	}
}

func TestRun_TightenWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only mode")
	}
	root := writeTree(t, map[string]string{"p/p.go": goSource(10, "F", 2)})
	baseline := filepath.Join(root, "baseline")
	if err := os.WriteFile(baseline, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(baseline, 0o400); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runIn(t, root, "-baseline", baseline, "-tighten")
	if code != 2 || !strings.Contains(stderr, "permission denied") {
		t.Fatalf("exit %d, want 2 with permission denied; stderr:\n%s", code, stderr)
	}
}

func TestCountLines(t *testing.T) {
	for src, want := range map[string]int{"": 0, "a\n": 1, "a\nb": 2, goSource(900, "F", 90): 900} {
		if got := countLines([]byte(src)); got != want {
			t.Errorf("countLines(%.20q) = %d, want %d", src, got, want)
		}
	}
}
