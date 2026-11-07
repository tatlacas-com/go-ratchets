# go-ratchets

Shrink-only code-health ratchets for Go repos: `sizelint` (file, function and
script size limits) and `archlint` (import-direction rules). Each keeps a
baseline of known violations that may only shrink: a new or grown violation
fails, and `-tighten` locks in fixes.

## `.ratchets.yml`

Both commands and `ratchets.Check` read this file from the repo root. Every
field is optional; a missing file means the defaults shown.

```yaml
limits: { file: 800, func: 80, script: 100 }   # lines
script_dirs: [ci, scripts]                      # trees whose *.sh are sized (default: none)
baselines:
  size: .ratchets/sizelint-baseline.txt         # default
  imports: .ratchets/archlint-baseline.txt      # default
rules:                                          # default: none
  - name: domain-is-framework-free
    from: [internal/domain/...]                 # importers (default: every package)
    forbid:                                     # imported packages
      - internal/adapter/...                    # module-local, relative to the module path
      - github.com/gin-gonic/gin                # third-party import-path prefix
      - github.com/jackc/pgx/
    why: the domain depends on interfaces, not frameworks
    # optional: except: [importers allowed], neutral: [packages anyone may import],
    #           own_tree: true (a "*" subtree may import itself)
```

Module-local patterns: `a/b` is that package, `a/b/...` it and everything below,
`*` any one path element. A `forbid` entry whose first element contains a dot is
a third-party import-path prefix, matched on whole path elements
(`github.com/gin-gonic/gin` covers `.../gin/binding`, not `.../ginx`). Standard
library imports are never flagged. Test files are not checked.

Baselines are plain text: sizelint lines are `<file|func|script> <path> <func or -> <lines>`,
archlint lines are `<rule> <importer> <imported>`. Both files must exist (empty is fine).

## Commands

```
go run github.com/tatlacas-com/go-ratchets/cmd/sizelint [-root .] [-baseline path] [-tighten]
go run github.com/tatlacas-com/go-ratchets/cmd/archlint [-root .] [-baseline path] [-tighten]
```

Both exit 1 on new or grown violations (archlint also on stale baseline
entries, until `-tighten`), and 2 on setup errors.

## Pinning in a consumer repo

```
go get -tool github.com/tatlacas-com/go-ratchets/cmd/sizelint@v0.1.0
go get -tool github.com/tatlacas-com/go-ratchets/cmd/archlint@v0.1.0
go tool sizelint && go tool archlint
go tool sizelint -tighten
```

The version is pinned in the consumer's `go.mod`.

## From `go test`

A violation then fails the repo's existing `go test ./...` job:

```go
func TestRatchets(t *testing.T) {
	cfg, err := ratchets.LoadConfig("..") // repo root
	if err != nil {
		t.Fatal(err)
	}
	problems, err := ratchets.Check("..", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
```

`Check` returns every new or grown size violation, every new import violation
and every stale import baseline entry. Shrunk size baseline entries are only
hinted at by the `sizelint` command, not failures.

## Dependencies

Stdlib, `golang.org/x/mod` and `gopkg.in/yaml.v3` (the stdlib has no YAML; it is
the small, standard choice).
