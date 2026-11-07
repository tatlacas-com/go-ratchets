// Package config reads the .ratchets.yml a consuming repo keeps at its root.
// Both ratchets take their limits, script dirs, baseline paths and import
// rules only from it.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the config file's name at the repo root.
const FileName = ".ratchets.yml"

// Default baseline paths, relative to the repo root.
const (
	DefaultSizeBaseline    = ".ratchets/sizelint-baseline.txt"
	DefaultImportsBaseline = ".ratchets/archlint-baseline.txt"
)

// Limits are the largest sizes, in lines, that pass without a baseline entry.
type Limits struct {
	File   int `yaml:"file"`
	Func   int `yaml:"func"`
	Script int `yaml:"script"`
}

// Baselines are the baseline file paths, relative to the repo root.
type Baselines struct {
	Size    string `yaml:"size"`
	Imports string `yaml:"imports"`
}

// Rule forbids packages matching From (every package when empty) from
// importing a package matching Forbid, except for importers matching Except.
// Module-local patterns are paths relative to the module path: "a/b" matches
// that package only, "a/b/..." it and every package below it, and a "*"
// element any one path element. A Forbid entry whose first element contains a
// dot is a third-party import-path prefix, such as "github.com/jackc/pgx/".
// With OwnTree set, an importer inside the same "*" subtree as the imported
// package is allowed. An imported package matching Neutral may be used by
// every importer.
type Rule struct {
	Name    string   `yaml:"name"`
	From    []string `yaml:"from"`
	Forbid  []string `yaml:"forbid"`
	Why     string   `yaml:"why"`
	Except  []string `yaml:"except"`
	Neutral []string `yaml:"neutral"`
	OwnTree bool     `yaml:"own_tree"`
}

// Config is the content of .ratchets.yml.
type Config struct {
	Limits     Limits    `yaml:"limits"`
	ScriptDirs []string  `yaml:"script_dirs"`
	Baselines  Baselines `yaml:"baselines"`
	Rules      []Rule    `yaml:"rules"`
}

// Defaults returns the config of a repo with no .ratchets.yml.
func Defaults() Config {
	return Config{
		Limits:    Limits{File: 800, Func: 80, Script: 100},
		Baselines: Baselines{Size: DefaultSizeBaseline, Imports: DefaultImportsBaseline},
	}
}

// Load reads root/.ratchets.yml. A missing file, or any field it leaves
// out, takes its default.
func Load(root string) (Config, error) {
	path := filepath.Join(root, FileName)
	f, err := os.Open(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = f.Close() }()
	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.fillDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// FillDefaults replaces every zero limit and empty baseline path with its
// default, for a Config built in code rather than loaded.
func (c *Config) FillDefaults() { c.fillDefaults() }

func (c *Config) fillDefaults() {
	d := Defaults()
	if c.Limits.File == 0 {
		c.Limits.File = d.Limits.File
	}
	if c.Limits.Func == 0 {
		c.Limits.Func = d.Limits.Func
	}
	if c.Limits.Script == 0 {
		c.Limits.Script = d.Limits.Script
	}
	if c.Baselines.Size == "" {
		c.Baselines.Size = d.Baselines.Size
	}
	if c.Baselines.Imports == "" {
		c.Baselines.Imports = d.Baselines.Imports
	}
}

// Validate reports the first thing wrong with c.
func (c *Config) Validate() error {
	if c.Limits.File < 0 || c.Limits.Func < 0 || c.Limits.Script < 0 {
		return errors.New("limits must be positive")
	}
	seen := map[string]bool{}
	for i, r := range c.Rules {
		switch {
		case r.Name == "" || strings.ContainsAny(r.Name, " \t\r\n"):
			return fmt.Errorf("rules[%d]: name must be non-empty and have no whitespace, got %q", i, r.Name)
		case seen[r.Name]:
			return fmt.Errorf("rules[%d]: duplicate rule name %q", i, r.Name)
		case len(r.Forbid) == 0:
			return fmt.Errorf("rule %s: forbid must list at least one pattern", r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

// Resolve returns the canonical form (cleaned, symlinks resolved) of path, absolute
// or joined to root, and refuses
// a result that lies outside root, whether by "..", an absolute path or a
// symlink, so a config cannot make the ratchets read or overwrite files
// elsewhere.
func Resolve(root, path string) (string, error) {
	full := path
	if !filepath.IsAbs(path) {
		full = filepath.Join(root, filepath.FromSlash(path))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	real, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("baseline path %q is outside the repo root", path)
	}
	return real, nil
}

// evalExisting resolves symlinks in the longest existing prefix of path and
// appends the rest, so a file yet to be written is still placed correctly.
func evalExisting(path string) (string, error) {
	var rest []string
	for p := path; ; {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if _, lerr := os.Lstat(p); lerr == nil {
			return "", fmt.Errorf("%s: dangling symlink", p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// Rule returns the rule called name.
func (c *Config) Rule(name string) (*Rule, bool) {
	for i := range c.Rules {
		if c.Rules[i].Name == name {
			return &c.Rules[i], true
		}
	}
	return nil, false
}
