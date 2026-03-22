# GoMake

`gomake` is a pure-Go Makefile runner for projects that want a self-contained binary without depending on `/bin/sh` at runtime.

## What It Supports

Supported inputs:

- `Makefile`
- `makefile`

Supported Makefile features:

- target dependencies
- `.PHONY`
- `.ONESHELL`
- `.NOTPARALLEL` (global/basic behavior)
- `.SILENT`
- `.IGNORE`
- `.DEFAULT` fallback recipes
- `.DELETE_ON_ERROR` basic cleanup behavior
- `.PRECIOUS` basic protection from delete-on-error cleanup
- `.DEFAULT_GOAL`
- `.RECIPEPREFIX`
- `include`, `-include`, and `sinclude`
- `define` / `endef` variable blocks
- `export` / `unexport` variable directives
- `ifdef`, `ifndef`, `ifeq`, `ifneq`, `else`, and `endif`
- simple variable assignments with `$(VAR)` and `${VAR}` expansion
- `:=`, `::=`, `?=`, `+=`, and `!=`
- basic target-specific variable assignments like `target: VAR = value`
- basic pattern-specific variable assignments like `%.txt: VAR = value`
- `override` assignments
- `private` assignments (basic compatibility)
- `undefine` directives
- `vpath` directives for prerequisite file lookup
- `VPATH` variable lookup for prerequisites
- environment variable import (with `-e` environment-precedence mode)
- built-in invocation variables: `MAKE`, `MAKECMDGOALS`, `MAKEFILE_LIST`, `MAKEFLAGS`, `MFLAGS`, `MAKELEVEL`
- order-only prerequisites with `$|`
- automatic variables in recipes: `$@`, `$<`, `$^`, `$+`, `$?`, `$*`, `$|`
- automatic variable variants like `$(@D)`, `$(@F)`, `$(<D)`, and `$(^F)`
- inline recipes after `;` in rule definitions
- built-in functions: `subst`, `patsubst`, `strip`, `findstring`, `filter`, `filter-out`, `sort`, `word`, `wordlist`, `words`, `firstword`, `lastword`, `join`, `dir`, `notdir`, `basename`, `suffix`, `addprefix`, `addsuffix`, `abspath`, `realpath`, `wildcard`, `if`, `or`, `and`, `foreach`, `call`, `value`, `origin`, `flavor`, `eval`, `shell`, `file`, `error`, `warning`, `info`
- recipe execution through a Go shell interpreter by default
- `SHELL` overrides that execute recipes through the configured external shell
- `.SHELLFLAGS` for external-shell invocation flags
- `.EXPORT_ALL_VARIABLES`
- command-line variable overrides such as `gomake MODE=release target`
- recipe prefixes `@`, `-`, and `+`
- dry-run recursive command detection for recipes containing `$(MAKE)` / `${MAKE}`
- `-k` keep-going mode to continue independent targets after failures
- `-n` dry-run mode (executes only forced `+` and recursive `$(MAKE)` commands, including under `.ONESHELL`)
- `-q` question mode (exit non-zero if targets are out of date)
- `-p` print database mode
- `-t` touch mode to update/create targets without running recipes
- `-W file` to treat files/targets as recently modified
- `--warn-undefined-variables` diagnostics during expansion
- file target freshness checks based on prerequisite timestamps
- basic recursive flag propagation through `MAKEFLAGS`

Current limits:

- not full GNU Make language compatibility
- no pattern rules or implicit rules
- target-specific and pattern-specific edge-case semantics are still being aligned with GNU Make
- no GNU jobserver support yet
- built-in function edge-case semantics are still being aligned with GNU Make
- recipe lines within a single target execute sequentially

## Commands

```bash
go run .
go run . -f ./examples/sample verify
go run . --version
make fmt
make lint
make unit
make integration
```

## Examples

`examples/sample` shows a Go-project style workflow with:

- a file target that builds a Go binary
- a phony verification target
- automatic variable usage via `$@` and `$<`

## CLI

- `gomake [options] [targets...]`: execute the default target or selected targets
- `gomake --version`

Supported options currently include `-f`, `-C`, `-j N`, `-s`, `-B`, `-e`, `-k`, `-n`, `-p`, `-q`, `-r`, `-R`, `-t`, `-W`, and `--warn-undefined-variables`.

Common GNU long aliases are accepted for these modes, including `--file`, `--directory`, `--jobs`, `--dry-run`, `--keep-going`, `--silent`, `--always-make`, `--print-data-base`, `--touch`, `--question`, and `--what-if`.

`-f` accepts either a directory containing a `Makefile`/`makefile` or an explicit path to one of those files.
