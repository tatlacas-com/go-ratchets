package main

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// edge is one direct import between two packages of the module, both as
// paths relative to the module root.
type edge struct {
	importer string
	imported string
}

// scan returns every direct import from a production Go file of the module at
// root to another package of the same module. As for the go tool, testdata,
// vendor and directories starting with "." or "_" are skipped, and so are
// nested modules, whose packages are not part of this one.
func scan(root string) (map[edge]bool, error) {
	// WalkDir does not follow a symlinked root, which would scan nothing and
	// let -tighten empty the baseline.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	modPath, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	edges := map[edge]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(root, path, d.Name())
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return addImports(root, modPath, path, edges)
	})
	if err != nil {
		return nil, err
	}
	return edges, nil
}

func skipDir(root, path, name string) error {
	if path == root {
		return nil
	}
	if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return filepath.SkipDir
	}
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return filepath.SkipDir
	}
	return nil
}

func addImports(root, modPath, path string, edges map[edge]bool) error {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	importer := filepath.ToSlash(filepath.Dir(relPath(root, path)))
	for _, spec := range f.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return fmt.Errorf("%s: import %s: %w", path, spec.Path.Value, err)
		}
		if rel, ok := strings.CutPrefix(imported, modPath+"/"); ok {
			edges[edge{importer: importer, imported: rel}] = true
		}
	}
	return nil
}

func relPath(root, path string) string {
	rel, _ := filepath.Rel(root, path) // path is under root, so Rel cannot fail
	return rel
}

func modulePath(goMod string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(goMod))
	if err != nil {
		return "", err
	}
	f, err := modfile.ParseLax(goMod, data, nil)
	if err != nil {
		return "", err
	}
	if f.Module == nil {
		return "", errors.New(goMod + ": no module directive")
	}
	path := f.Module.Mod.Path
	if err := module.CheckImportPath(path); err != nil {
		return "", fmt.Errorf("%s: %w", goMod, err)
	}
	return path, nil
}
