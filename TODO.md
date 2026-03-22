# GNU Make Compatibility TODO

This is the active, prioritized backlog for GNU Make parity in `gomake`.

Legend:

- [x] implemented
- [~] partially implemented (works for common cases, edge parity pending)
- [ ] not implemented

## Baseline Implemented

These are done and should remain regression-tested.

- [x] explicit rules with normal prerequisites
- [x] order-only prerequisites
- [x] `.PHONY`
- [x] `.ONESHELL` (including prefix handling parity)
- [x] `.NOTPARALLEL` (full global serialization)
- [x] `.SILENT`
- [x] `.IGNORE`
- [x] `include`, `-include`, `sinclude`
- [x] `ifdef`, `ifndef`, `ifeq`, `ifneq`, `else`, `endif` (including `else if...` nesting)
- [x] variable assignment operators `=`, `:=`, `::=`, `?=`, `+=`, `!=`
- [x] target-specific variable assignments (full inheritance and runtime expansion)
- [x] pattern-specific variable assignments (full inheritance and runtime expansion)
- [x] `$(VAR)` and `${VAR}` expansion (including deferred runtime recipe expansion)
- [x] command-line variable overrides (for example: `gomake MODE=release target`)
- [x] environment variable import with makefile precedence and `-e` mode
- [x] invocation variables `MAKE`, `MAKECMDGOALS`, `MAKEFILE_LIST`, `MAKEFLAGS`, `MFLAGS`
- [x] `MAKELEVEL` propagation
- [x] `MAKEFLAGS` propagation to recursive recipe environments
- [x] automatic variables `$@`, `$<`, `$^`, `$+`, `$?`, `$*`, `$|`, `$%`
- [x] automatic variable variants `$(@D)`, `$(@F)`, `$(<D)`, `$(<F)`, `$(^D)`, `$(^F)`, `$(+D)`, `$(+F)`, `$(?D)`, `$(?F)`, `$(*D)`, `$(*F)`
- [x] recipe prefixes `@`, `-`, `+` (normalization and parity behavior)
- [x] recursive command detection for `$(MAKE)` / `${MAKE}` in dry-run mode
- [x] embedded shell execution by default
- [x] external shell execution when `SHELL` is set
- [x] `.SHELLFLAGS`
- [x] `.DEFAULT` fallback rule behavior
- [x] `.DELETE_ON_ERROR` cleanup behavior (including signal handling)
- [x] `.PRECIOUS` delete protection behavior
- [x] `.DEFAULT_GOAL`
- [x] `.RECIPEPREFIX`
- [x] inline recipes after `;`
- [x] `define` / `endef`
- [x] `override` assignments
- [x] `private` directives (including inheritance parity)
- [x] `undefine`
- [x] `export` / `unexport` directives
- [x] `vpath` directives (full lookup parity)
- [x] `VPATH` support (full lookup parity)
- [x] `.EXPORT_ALL_VARIABLES`
- [x] `-k` keep-going mode
- [x] `-i` ignore-errors mode
- [x] `-n` dry-run mode including forced `+` and recursive `$(MAKE)` handling
- [x] `-q` question mode
- [x] `-t` touch mode
- [x] `-W` what-if mode
- [x] `-r` no-builtin-rules
- [x] `-R` no-builtin-vars
- [x] `-p` print-database
- [x] `-C` multiple directory changes
- [x] `--warn-undefined-variables`
- [x] function set implemented, edge-case parity mostly complete
- [x] basic freshness checks for explicit file targets

## Priority Roadmap

Implementation order is intentional: each stage reduces risk for the next one.

### P0: Semantics Correctness (highest priority)

- [x] full GNU variable precedence parity across command-line, `override`, target/pattern scope, makefile, environment, and built-ins
- [x] target-specific variable edge parity (command-line precedence, target-scoped `override` interactions, and inheritance depth implemented)
- [x] pattern-specific variables (full support including inheritance implemented)
- [x] append semantics parity across all precedence combinations
- [x] single-character variable reference behavior in all contexts
- [x] recursive vs simple expansion parity in edge cases
- [x] expansion behavior for target and prerequisite lists
- [x] escaping and quoting parity across parser and runtime contexts
- [x] full nested expansion parity for `call` and `eval`
- [x] secondary expansion support (`.SECONDEXPANSION` full prerequisite expansion parity)
- [x] function edge-case parity for diagnostics and nested interactions

Exit criteria for P0:

- [x] fixture coverage for each precedence tier and expansion edge class
- [x] differential tests versus GNU Make for all newly supported behavior

### P1: Rule Language and Resolver

- [x] pattern rules (for example: `%.o: %.c`)
- [x] static pattern rules
- [x] built-in implicit-rule database
- [x] implicit rule resolution engine
- [x] chained implicit rule resolution
- [x] intermediate target handling during implicit builds
- [x] grouped targets and full multi-target GNU semantics
- [x] double-colon rule semantics (`::`)
- [x] archive/member targets such as `lib.a(obj.o)`
- [x] suffix rule compatibility
- [x] `.SUFFIXES` semantics

Exit criteria for P1:

- [x] differential fixtures proving resolver selection order matches GNU Make
- [x] out-of-date checks validated for implicit and intermediate graphs

### P2: Execution Model and Special Targets

- [x] `.DEFAULT` edge parity with implicit-rule and rebuild interactions
- [x] `.SECONDEXPANSION`
- [x] `.DELETE_ON_ERROR` timing and partial-update parity
- [x] `.PRECIOUS` edge parity beyond basic protection
- [x] `.INTERMEDIATE`
- [x] `.SECONDARY`
- [x] `.NOTINTERMEDIATE`
- [x] `.NOTPARALLEL` (full global behavior)
- [x] `.LOW_RESOLUTION_TIME`
- [x] `.POSIX` (including env-override parity)
- [x] `.ONESHELL` edge parity including prefix handling
- [x] per-line versus `.ONESHELL` execution parity in edge cases
- [x] shell selection and invocation parity beyond plain `SHELL -c`
- [x] recursive make semantics around `$(MAKE)`
- [x] `+` prefix behavior parity for recursive execution
- [x] dry-run parity for recursive edge cases
- [x] command echo and silencing parity under all flag combinations and special targets
- [x] interrupt handling and cleanup parity

Exit criteria for P2:

- [x] fixture suites for special targets and shell execution edge cases
- [x] deterministic behavior under signal interruption tests

### P3: CLI, Parallelism, and Recursive Propagation

- [x] `-j` parallel execution (local scheduler implemented)
- [ ] GNU jobserver client (token acquisition from inherited pipe)
- [ ] GNU jobserver server (pipe creation and token management)
- [ ] jobserver descriptor propagation to recursive invocations
- [x] full `-C` and `-f` interaction parity
- [x] broad long-option compatibility with GNU Make
- [x] command-line variable export behavior parity
- [x] `MAKEFLAGS` propagation edge-case parity
- [x] exported variable propagation rules in recursive make
- [x] complete `$(MAKE)` special handling rules

### P4: Dependency/Rebuild and Parser Coverage

- [x] implicit prerequisite discovery parity
- [x] accurate stale checks for implicit and intermediate targets
- [x] low-resolution timestamp handling
- [x] order and duplicate prerequisite semantics parity (`$^` versus `$+`)
- [x] phony prerequisite handling in complex rule graphs
- [x] richer comment and escaping edge cases
- [x] continued-line behavior parity in all contexts
- [x] full conditional spelling and parser edge-case parity
- [x] advanced directive syntax and whitespace tolerance parity

### P5: Built-Ins, Metadata, and Docs

- [x] default implicit-rule variables (`CC`, `CXX`, `AR`, `RM`, related flags)
- [x] shell-related built-ins with GNU semantics
- [x] directory/file metadata internals required by implicit-rule engine
- [ ] update `README.md` compatibility matrix to reflect massive P0-P2 progress
- [ ] document shell selection behavior (embedded vs external) and host requirements
- [ ] document supported subset versus unsupported features (e.g. `load`)
- [ ] add comprehensive examples for advanced patterns and recursive workflows

## Global Done Criteria

The compatibility effort is considered complete when all of the following are true:

- [ ] GNU Jobserver (P3) is fully implemented and tested across recursive calls
- [x] differential suite shows no known behavioral drift for in-scope features
- [ ] persistent regression fixtures cover every one of the 70+ implemented compat points
- [ ] documentation describes behavior with 100% accuracy, including edge cases
