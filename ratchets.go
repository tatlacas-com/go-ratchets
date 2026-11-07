// Package ratchets enforces the shrink-only code-health ratchets, size limits
// and import-direction rules, from an ordinary Go test, so a violation fails
// the repo's existing `go test ./...` job:
//
//	func TestRatchets(t *testing.T) {
//		cfg, err := ratchets.LoadConfig("..")
//		if err != nil {
//			t.Fatal(err)
//		}
//		problems, err := ratchets.Check("..", cfg)
//		if err != nil {
//			t.Fatal(err)
//		}
//		for _, p := range problems {
//			t.Error(p)
//		}
//	}
//
// It does what the sizelint and archlint commands do, with the same
// implementation, and is configured by the same .ratchets.yml at the repo root.
package ratchets

import (
	"github.com/tatlacas-com/go-ratchets/internal/archlint"
	"github.com/tatlacas-com/go-ratchets/internal/config"
	"github.com/tatlacas-com/go-ratchets/internal/sizelint"
)

// Config is the content of .ratchets.yml: limits, script dirs, baseline
// paths and import rules.
type Config = config.Config

// Rule is one import-direction rule of a Config.
type Rule = config.Rule

// LoadConfig reads root/.ratchets.yml. A missing file gives the defaults:
// limits of 800 lines per file, 80 per function and 100 per script, no
// script dirs, baselines under .ratchets/ and no rules.
func LoadConfig(root string) (Config, error) { return config.Load(root) }

// Check runs both ratchets on the module at root and returns a problem for
// every new or grown size violation, every new import violation and every
// stale import baseline entry. Size baseline entries that merely shrank or
// went are not problems, as for the sizelint command. A missing baseline
// file is an error.
func Check(root string, cfg Config) ([]string, error) {
	cfg.FillDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	problems, err := sizelint.Check(root, &cfg)
	if err != nil {
		return nil, err
	}
	more, err := archlint.Check(root, &cfg)
	if err != nil {
		return nil, err
	}
	return append(problems, more...), nil
}
