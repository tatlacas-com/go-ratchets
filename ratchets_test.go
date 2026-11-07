package ratchets_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ratchets "github.com/tatlacas-com/go-ratchets"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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

func check(t *testing.T, root string) []string {
	t.Helper()
	cfg, err := ratchets.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	problems, err := ratchets.Check(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func goFile(lines int) string {
	return "package p\n" + strings.Repeat("\n", lines-1)
}

const goMod = "module example.com/fix\n\ngo 1.26\n"

func TestCheck_ReadsLimitsScriptDirsAndBaselinesFromTheRepoConfig(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod": goMod,
		".ratchets.yml": "limits: { file: 10, func: 5, script: 3 }\n" +
			"script_dirs: [ci]\n" +
			"baselines: { size: lint/size.txt, imports: lint/imports.txt }\n",
		"lint/size.txt":    "file p/big.go - 12\n",
		"lint/imports.txt": "",
		"p/big.go":         goFile(12),
		"p/ok.go":          goFile(10),
		"ci/long.sh":       "a\nb\nc\nd\n",
		"other/long.sh":    "a\nb\nc\nd\n",
	})
	problems := check(t, root)
	if len(problems) != 1 || !strings.Contains(problems[0], "script ci/long.sh: 4 lines exceeds the 3-line limit") {
		t.Fatalf("problems = %q, want only the ci script over its 3-line limit", problems)
	}
}

func TestCheck_ThirdPartyForbidRuleFlagsTheImport(t *testing.T) {
	files := map[string]string{
		"go.mod": goMod,
		".ratchets.yml": `rules:
  - name: domain-no-web
    from: [internal/domain/...]
    forbid: [github.com/gin-gonic/gin, "github.com/jackc/pgx/"]
    why: the domain stays framework-free
`,
		".ratchets/sizelint-baseline.txt": "",
		".ratchets/archlint-baseline.txt": "",
		"internal/domain/d.go":            "package domain\n\nimport _ \"github.com/gin-gonic/gin/binding\"\nimport _ \"github.com/jackc/pgx/v5\"\nimport _ \"fmt\"\nimport _ \"github.com/gin-gonic/ginx\"\n",
		"internal/http/h.go":              "package http\n\nimport _ \"github.com/gin-gonic/gin\"\n",
	}
	problems := check(t, writeTree(t, files))
	want := []string{
		"domain-no-web: internal/domain imports github.com/gin-gonic/gin/binding, but the domain stays framework-free",
		"domain-no-web: internal/domain imports github.com/jackc/pgx/v5, but the domain stays framework-free",
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %q, want %q", problems, want)
	}
	for i, w := range want {
		if !strings.HasPrefix(problems[i], w) {
			t.Errorf("problem %d = %q, want prefix %q", i, problems[i], w)
		}
	}

	// The baseline applies to a third-party edge like any other.
	files[".ratchets/archlint-baseline.txt"] = "domain-no-web internal/domain github.com/gin-gonic/gin/binding\n" +
		"domain-no-web internal/domain github.com/jackc/pgx/v5\n"
	if problems := check(t, writeTree(t, files)); len(problems) != 0 {
		t.Fatalf("baselined problems = %q, want none", problems)
	}
}

func TestCheck_NewOrGrownViolationAndStaleBaselineEntryFail(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod": goMod,
		".ratchets.yml": `limits: { file: 10 }
rules:
  - name: no-cmd
    from: [internal/...]
    forbid: [cmd/...]
    why: internal never imports cmd
`,
		".ratchets/sizelint-baseline.txt": "file p/grown.go - 12\nfile p/shrunk.go - 20\n",
		".ratchets/archlint-baseline.txt": "no-cmd internal/gone cmd/app\n",
		"p/grown.go":                      goFile(13),
		"p/shrunk.go":                     goFile(15),
		"p/new.go":                        goFile(11),
		"internal/a/a.go":                 "package a\n\nimport _ \"example.com/fix/cmd/app\"\n",
	})
	problems := strings.Join(check(t, root), "\n")
	for _, want := range []string{
		"file p/grown.go: 13 lines grew past its baseline of 12",
		"file p/new.go: 11 lines exceeds the 10-line limit and is not baselined",
		"no-cmd: internal/a imports cmd/app",
		"no-cmd internal/gone cmd/app: baselined but no longer found",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems lack %q:\n%s", want, problems)
		}
	}
	if strings.Contains(problems, "shrunk.go") {
		t.Errorf("a shrunk baseline entry is not a problem:\n%s", problems)
	}
}

func TestLoadConfig_MissingFileFallsBackToDefaultsWithNoRules(t *testing.T) {
	cfg, err := ratchets.LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.File != 800 || cfg.Limits.Func != 80 || cfg.Limits.Script != 100 {
		t.Errorf("limits = %+v, want 800/80/100", cfg.Limits)
	}
	if len(cfg.ScriptDirs) != 0 || len(cfg.Rules) != 0 {
		t.Errorf("script dirs %q, rules %v; want none", cfg.ScriptDirs, cfg.Rules)
	}
	if cfg.Baselines.Size != ".ratchets/sizelint-baseline.txt" || cfg.Baselines.Imports != ".ratchets/archlint-baseline.txt" {
		t.Errorf("baselines = %+v, want the .ratchets/ defaults", cfg.Baselines)
	}
}

func TestLoadConfig_RejectsUnknownKeysAndBadRules(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key": "limit: {file: 1}\n",
		"no forbid":   "rules: [{name: r}]\n",
		"no name":     "rules: [{forbid: [a]}]\n",
		"duplicate":   "rules: [{name: r, forbid: [a]}, {name: r, forbid: [b]}]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ratchets.LoadConfig(writeTree(t, map[string]string{".ratchets.yml": body})); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestCheck_ThisRepoPassesItsOwnRatchets(t *testing.T) {
	if problems := check(t, "."); len(problems) != 0 {
		t.Fatalf("problems:\n%s", strings.Join(problems, "\n"))
	}
}
