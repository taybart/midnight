# Contributing

## Layout

| Path | What |
| --- | --- |
| `parse/midnight_lexer.go`, `parse/midnight_parser.go` | Midnight parser (Midnight -> AST) |
| `luagen/` | Lua code generator (AST -> Lua) |
| `midnight.go`, `bit32lib.go` | Midnight Go API, `bit32` library |
| `cmd/midnight/` | CLI |
| everything else | gopher-lua (Lua parser, compiler, VM, standard libraries) |

## Keeping in sync w/ gopher-lua

midnight should stay mergeable w/ upstream gopher-lua, so:

- Don't change the compiler or VM. Midnight features are lowered to the existing AST
  in the parser (see [docs/PORTING.md](../docs/PORTING.md)).
- Put Midnight additions in new files. When an upstream file has to change, keep the
  change small and mark it w/ a `midnight` comment.

To merge upstream changes:

```sh
git remote add upstream https://github.com/yuin/gopher-lua.git
git fetch upstream
git merge upstream/master
# conflicts should only be import paths (github.com/yuin/gopher-lua -> github.com/taybart/midnight)
```

## Building

Files starting w/ `_` generate the file of the same name without it (ex.
`_state.go` -> `state.go`) via a small inlining tool (requires python). Don't
edit the generated files directly.

```sh
make build # regenerate + build
make midnight  # build the CLI
make test
```

## Testing

See [Testing](../README.md#testing) in the README. When changing the Midnight
parser, also run `_tools/midnight-spec/selfhost.sh <erde-lang/erde checkout>`.
