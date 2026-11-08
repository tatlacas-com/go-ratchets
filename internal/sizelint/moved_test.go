package sizelint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatlacas-com/go-ratchets/internal/config"
)

func fnSource(fn string, bodyLines int) string {
	return "package p\n\nfunc " + fn + "() {\n" + strings.Repeat("\t_ = 0\n", bodyLines-2) + "}\n"
}

func movedTree(t *testing.T, baseline string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files[config.FileName] = ""
	files[config.DefaultSizeBaseline] = baseline
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

func runMoved(root string, args ...string) (int, string) {
	var out, errOut bytes.Buffer
	code := Run(append([]string{"-root", root}, args...), &out, &errOut)
	return code, errOut.String()
}

func TestTighten_MovedFunctionKeepsItsAllowance(t *testing.T) {
	root := movedTree(t, "func p/a.go F 100\n", map[string]string{
		"p/a.go": "package p\n",
		"p/b.go": fnSource("F", 90),
	})
	if code, stderr := runMoved(root); code != 0 {
		t.Fatalf("check exit %d: %s", code, stderr)
	}
	if code, stderr := runMoved(root, "-tighten"); code != 0 {
		t.Fatalf("tighten exit %d: %s", code, stderr)
	}
	got, err := os.ReadFile(filepath.Join(root, config.DefaultSizeBaseline))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "func p/b.go F 90\n") || strings.Contains(string(got), "p/a.go") {
		t.Fatalf("baseline:\n%s", got)
	}
}

func TestCheck_MovedFunctionSameSize(t *testing.T) {
	root := movedTree(t, "func p/a.go F 90\n", map[string]string{
		"p/a.go": "package p\n",
		"p/b.go": fnSource("F", 90),
	})
	if code, stderr := runMoved(root); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestCheck_MovedFunctionStillFails(t *testing.T) {
	cases := map[string]map[string]string{
		"grown":         {"p/a.go": "package p\n", "p/b.go": fnSource("F", 95)},
		"cross-package": {"p/a.go": "package p\n", "q/b.go": fnSource("F", 85)},
		"copied":        {"p/a.go": fnSource("F", 90), "p/b.go": fnSource("F", 85)},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			root := movedTree(t, "func p/a.go F 90\n", files)
			code, stderr := runMoved(root, "-tighten")
			if code != 1 || !strings.Contains(stderr, "refusing to tighten") {
				t.Fatalf("exit %d: %s", code, stderr)
			}
		})
	}
}
