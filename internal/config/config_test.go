package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMakefile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "GREETING = hello\nall: app\n\napp: input.txt\n\tprintf '%s\\n' '$@ $< $(GREETING)' > app\n\n.PHONY: all\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if project.Format != FormatMakefile {
		t.Fatalf("Format = %q, want makefile", project.Format)
	}
	if project.DefaultTarget != "all" {
		t.Fatalf("DefaultTarget = %q, want all", project.DefaultTarget)
	}
	if len(project.Targets) != 3 {
		t.Fatalf("len(Targets) = %d, want 3", len(project.Targets))
	}
	if project.Targets[1].Name != "app" {
		t.Fatalf("second target = %q, want app", project.Targets[1].Name)
	}
	// Recipe variable expansion is deferred to build time, so stored text is verbatim.
	if len(project.Targets[1].Commands) != 1 || project.Targets[1].Commands[0].Text != "printf '%s\\n' '$@ $< $(GREETING)' > app" {
		t.Fatalf("commands = %v", project.Targets[1].Commands)
	}
}

func TestDetectProjectFilePrefersMakefile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"Makefile": "all:\n\tprintf 'ok'\n",
		"makefile": "all:\n\tprintf 'lowercase'\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if project.SourcePath != filepath.Join(dir, "Makefile") {
		t.Fatalf("SourcePath = %q, want %q", project.SourcePath, filepath.Join(dir, "Makefile"))
	}
	if got := project.Variables["CURDIR"]; got != dir {
		t.Fatalf("CURDIR = %q, want %q", got, dir)
	}
}

func TestLoadMakefileNormalizesRecipePrefixes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all:\n\t@echo hidden\n\t-false\n\t+echo forced\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var commands []string
	for _, command := range project.Targets[0].Commands {
		commands = append(commands, command.Text)
	}
	got := strings.Join(commands, "|")
	want := "echo hidden|false|echo forced"
	if got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	if !project.Targets[0].Commands[1].IgnoreError {
		t.Fatalf("second command should ignore errors")
	}
	if !project.Targets[0].Commands[2].Force {
		t.Fatalf("third command should be forced")
	}
}

func TestLoadMarksRecursiveMakeCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all:\n\t$(MAKE) child\n\t${MAKE} other\n\tprintf done\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	commands := project.Targets[0].Commands
	if len(commands) != 3 {
		t.Fatalf("len(commands) = %d, want 3", len(commands))
	}
	if !commands[0].Recursive || !commands[1].Recursive {
		t.Fatalf("first two commands should be recursive: %#v", commands)
	}
	if commands[2].Recursive {
		t.Fatalf("third command should not be recursive")
	}
}

func TestLoadRejectsUnsupportedExplicitFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gomake.json")
	if err := os.WriteFile(path, []byte(`{"targets":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "only Makefile and makefile are supported") {
		t.Fatalf("Load() error = %v, want unsupported file error", err)
	}
}

func TestLoadRejectsDirectoryWithoutMakefile(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "no supported project file found") {
		t.Fatalf("Load() error = %v, want missing makefile error", err)
	}
}

func TestLoadInclude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vars.mk"), []byte("MSG = included\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(vars.mk) error = %v", err)
	}
	content := "include vars.mk\nall:\n\tprintf '%s' '$(MSG)'\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(Makefile) error = %v", err)
	}

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "included" {
		t.Fatalf("MSG = %q, want included", got)
	}
}

func TestLoadOptionalIncludeMissing(t *testing.T) {
	dir := t.TempDir()
	content := "-include missing.mk\nall:\n\tprintf ok\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(Makefile) error = %v", err)
	}

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadConditionals(t *testing.T) {
	dir := t.TempDir()
	content := "MODE = debug\nifdef MODE\nMSG = on\nelse\nMSG = off\nendif\nifeq ($(MODE), debug)\nFLAG = yes\nendif\nall:\n\tprintf '%s %s' '$(MSG)' '$(FLAG)'\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(Makefile) error = %v", err)
	}

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "on" {
		t.Fatalf("MSG = %q, want on", got)
	}
	if got := project.Variables["FLAG"]; got != "yes" {
		t.Fatalf("FLAG = %q, want yes", got)
	}
}

func TestLoadStripsInlineComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all: input.txt # comment\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Targets[0].Deps; len(got) != 1 || got[0] != "input.txt" {
		t.Fatalf("deps = %v, want [input.txt]", got)
	}
}

func TestLoadAssignmentOperators(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MSG = hello\nMSG += world\nMSG ?= ignored\nCOUNT != printf '1\\n2\\n'\nall:\n\tprintf '%s|%s' '$(MSG)' '$(COUNT)'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "hello world" {
		t.Fatalf("MSG = %q, want hello world", got)
	}
	if got := project.Variables["COUNT"]; got != "1 2" {
		t.Fatalf("COUNT = %q, want 1 2", got)
	}
}

func TestLoadBangEqualsAssignmentIsRecursive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "NAME = world\nMSG != printf 'hello $(NAME)\\nnext\\n'\nNAME = there\nFLAVOR := $(flavor MSG)\nEXPANDED := $(MSG)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["FLAVOR"]; got != "recursive" {
		t.Fatalf("FLAVOR = %q, want recursive", got)
	}
	if got := project.Variables["EXPANDED"]; got != "hello world next" {
		t.Fatalf("EXPANDED = %q, want hello world next", got)
	}
}

func TestLoadDistinguishesRecursiveAndImmediateAssignments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "NAME = world\nRECURSIVE = hello $(NAME)\nIMMEDIATE := hello $(NAME)\nDOUBLECOLON ::= hello $(NAME)\nNAME = there\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["RECURSIVE"]; got != "hello there" {
		t.Fatalf("RECURSIVE = %q, want hello there", got)
	}
	if got := project.Variables["IMMEDIATE"]; got != "hello world" {
		t.Fatalf("IMMEDIATE = %q, want hello world", got)
	}
	if got := project.Variables["DOUBLECOLON"]; got != "hello world" {
		t.Fatalf("DOUBLECOLON = %q, want hello world", got)
	}
}

func TestLoadOneShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".ONESHELL:\nall:\n\tfoo=bar\n\tprintf '%s' \"$$foo\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !project.OneShell {
		t.Fatalf("OneShell = false, want true")
	}
}

func TestLoadNotParallelSpecialTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".NOTPARALLEL:\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !project.NotParallel {
		t.Fatalf("NotParallel = false, want true")
	}
}

func TestLoadQuotedConditionals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = debug\nifeq \"$(MODE)\" \"debug\"\nMSG = on\nendif\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "on" {
		t.Fatalf("MSG = %q, want on", got)
	}
}

func TestLoadDefineEndef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "define SCRIPT\necho one\necho two\nendef\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["SCRIPT"]; got != "echo one\necho two" {
		t.Fatalf("SCRIPT = %q, want multiline define content", got)
	}
}

func TestLoadDefineEndefWithImmediateAssignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "NAME = world\ndefine MSG :=\nhello $(NAME)\nendef\nNAME = there\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "hello world" {
		t.Fatalf("MSG = %q, want immediate define expansion", got)
	}
}

func TestLoadRejectsUnterminatedDefine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "define MSG\nhello\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unterminated define block") {
		t.Fatalf("Load() error = %v, want unterminated define error", err)
	}
}

func TestLoadSupportsBraceVariableSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = release\nMSG = ${MODE}\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "release" {
		t.Fatalf("MSG = %q, want release", got)
	}
}

func TestLoadSupportsSingleCharacterVariableRefsAndEscapedDollars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "H = hi\nX = make\nSINGLE := $X\nLONG := $HOME\nESCAPED := $$HOME\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := map[string]string{
		"SINGLE":  "make",
		"LONG":    "hiOME",
		"ESCAPED": "$HOME",
	}
	for key, want := range checks {
		if got := project.Variables[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadValueFunctionPreservesUnexpandedRecursiveTextInSimpleVariable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "X = one\nY = $(X)\nV := $(value Y)\nall:\n\tprintf '%s' '$(V)' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["V"]; got != "$(X)" {
		t.Fatalf("V = %q, want literal $(X) preserved by the value function", got)
	}
}

// GNU make aborts with "recursive variable references itself" when such a
// variable is expanded. gomake instead flattens the self-reference to empty at
// parse time, so loading succeeds and the variable resolves without the
// offending reference. Tracked as a known limitation in TODO.md.
func TestLoadSelfReferentialRecursiveVariableFlattensToEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "A = $(A) x\nall:\n\tprintf '%s' '$(A)' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["A"]; got != " x" {
		t.Fatalf("A = %q, want %q (self-reference flattened to empty)", got, " x")
	}
}

func TestLoadAllowsUnusedRecursiveVariableDefinition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "A = $(A) x\nall:\n\tprintf '%s' 'ok' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.Targets) != 1 || len(project.Targets[0].Commands) != 1 {
		t.Fatalf("targets = %#v, want one target with one command", project.Targets)
	}
	if got := project.Targets[0].Commands[0].Text; got != "printf '%s' 'ok' > out.txt" {
		t.Fatalf("command = %q, want unaffected recipe", got)
	}
}

func TestLoadSupportsEvalGeneratedRuleFromDefine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "define RULE\ndynamic:\n\tprintf 'generated' > dynamic.txt\nendef\n$(eval $(RULE))\nall: dynamic\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	all, ok := targets["all"]
	if !ok {
		t.Fatalf("targets = %v, want all target", project.Targets)
	}
	if len(all.Deps) != 1 || all.Deps[0] != "dynamic" {
		t.Fatalf("all deps = %v, want [dynamic]", all.Deps)
	}
	dynamic, ok := targets["dynamic"]
	if !ok {
		t.Fatalf("targets = %v, want dynamic target", project.Targets)
	}
	if len(dynamic.Commands) != 1 || dynamic.Commands[0].Text != "printf 'generated' > dynamic.txt" {
		t.Fatalf("dynamic commands = %#v, want eval-generated recipe", dynamic.Commands)
	}
}

func TestLoadSupportsEvalGeneratedInlineRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "$(eval generated: ; printf 'ok' > out.txt)\nall: generated\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	generated, ok := targets["generated"]
	if !ok {
		t.Fatalf("targets = %v, want generated target", project.Targets)
	}
	if len(generated.Commands) != 1 || generated.Commands[0].Text != "printf 'ok' > out.txt" {
		t.Fatalf("generated commands = %#v, want inline eval-generated recipe", generated.Commands)
	}
}

func TestLoadSeedsBuiltinInvocationVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all:\n\tprintf '%s|%s|%s' '$(MAKE)' '$(MAKECMDGOALS)' '$(MAKEFILE_LIST)'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithContext(dir, LoadContext{
		Goals:       []string{"all", "test"},
		MakeProgram: "/tmp/bin/gomake-test",
	})
	if err != nil {
		t.Fatalf("LoadWithContext() error = %v", err)
	}

	if got := project.Variables["MAKE"]; got != "gomake-test" {
		t.Fatalf("MAKE = %q, want gomake-test", got)
	}
	if got := project.Variables["MAKECMDGOALS"]; got != "all test" {
		t.Fatalf("MAKECMDGOALS = %q, want all test", got)
	}
	if got := project.Variables["MAKEFILE_LIST"]; got != path {
		t.Fatalf("MAKEFILE_LIST = %q, want %q", got, path)
	}
	// Recipe expansion is deferred, so the stored recipe keeps the raw references.
	if got := project.Targets[0].Commands[0].Text; got != "printf '%s|%s|%s' '$(MAKE)' '$(MAKECMDGOALS)' '$(MAKEFILE_LIST)'" {
		t.Fatalf("command = %q", got)
	}
}

func TestLoadTracksIncludedFilesInMakefileList(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "Makefile")
	includePath := filepath.Join(dir, "config.mk")
	content := "include config.mk\nSNAPSHOT := $(MAKEFILE_LIST)\nall:\n\tprintf '%s' '$(SNAPSHOT)'\n"
	if err := os.WriteFile(includePath, []byte("MSG = ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config.mk) error = %v", err)
	}
	if err := os.WriteFile(mainPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(Makefile) error = %v", err)
	}

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := mainPath + " " + includePath
	if got := project.Variables["MAKEFILE_LIST"]; got != want {
		t.Fatalf("MAKEFILE_LIST = %q, want %q", got, want)
	}
	if got := project.Variables["SNAPSHOT"]; got != want {
		t.Fatalf("SNAPSHOT = %q, want %q", got, want)
	}
}

func TestLoadSeedsBuiltinFlagVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithContext(path, LoadContext{MakeFlags: "-k --warn-undefined-variables MODE=release"})
	if err != nil {
		t.Fatalf("LoadWithContext() error = %v", err)
	}
	if got := project.Variables["MAKEFLAGS"]; got != "-k --warn-undefined-variables MODE=release" {
		t.Fatalf("MAKEFLAGS = %q, want propagated flags", got)
	}
	if got := project.Variables["MFLAGS"]; got != "-k --warn-undefined-variables MODE=release" {
		t.Fatalf("MFLAGS = %q, want propagated flags", got)
	}
}

func TestLoadSeedsMakelevel(t *testing.T) {
	t.Setenv("MAKELEVEL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(path, []byte("all:\n\tprintf ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MAKELEVEL"]; got != "0" {
		t.Fatalf("MAKELEVEL = %q, want 0", got)
	}
}

func TestLoadIncrementsInheritedMakelevel(t *testing.T) {
	t.Setenv("MAKELEVEL", "2")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(path, []byte("all:\n\tprintf ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MAKELEVEL"]; got != "3" {
		t.Fatalf("MAKELEVEL = %q, want 3", got)
	}
}

func TestLoadWithOverridesUsesCommandLineValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = debug\nMSG = $(MODE)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithOverrides(path, map[string]string{"MODE": "release"})
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if got := project.Variables["MODE"]; got != "release" {
		t.Fatalf("MODE = %q, want release", got)
	}
	if got := project.Variables["MSG"]; got != "release" {
		t.Fatalf("MSG = %q, want release", got)
	}
}

func TestLoadOverrideDirectiveBeatsCommandLineOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = file\noverride MODE = forced\nMSG := $(MODE)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithOverrides(path, map[string]string{"MODE": "cli"})
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if got := project.Variables["MODE"]; got != "forced" {
		t.Fatalf("MODE = %q, want forced", got)
	}
	if got := project.Variables["MSG"]; got != "forced" {
		t.Fatalf("MSG = %q, want forced", got)
	}
}

func TestLoadOverrideDirectivePreventsLaterNormalAssignments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "override MODE = forced\nMODE = later\nMSG := $(MODE)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MODE"]; got != "forced" {
		t.Fatalf("MODE = %q, want forced", got)
	}
	if got := project.Variables["MSG"]; got != "forced" {
		t.Fatalf("MSG = %q, want forced", got)
	}
}

func TestLoadOverrideDirectiveSupportsAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "override FLAGS = a\noverride FLAGS += b\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["FLAGS"]; got != "a b" {
		t.Fatalf("FLAGS = %q, want a b", got)
	}
}

func TestLoadRejectsOverrideWithoutAssignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "override MODE\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "override directive requires variable assignment") {
		t.Fatalf("Load() error = %v, want override syntax error", err)
	}
}

func TestLoadPrivateDirectiveAppliesAssignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = debug\nprivate MODE := release\nMSG := $(MODE)\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := project.Variables["MODE"]; ok {
		t.Fatalf("MODE should be private and omitted from final variables, got %q", project.Variables["MODE"])
	}
	if got := project.Variables["MSG"]; got != "release" {
		t.Fatalf("MSG = %q, want release", got)
	}
	if len(project.Targets) != 1 || len(project.Targets[0].Commands) != 1 {
		t.Fatalf("targets = %#v, want one target with one command", project.Targets)
	}
	// Recipe text is stored verbatim; the private variable's effect on the
	// recipe is verified end-to-end in the build package.
	if got := project.Targets[0].Commands[0].Text; got != "printf '%s' '$(MODE)' > out.txt" {
		t.Fatalf("command = %q, want verbatim recipe text", got)
	}
}

func TestLoadRejectsPrivateWithoutAssignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "private MODE\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "private directive requires variable assignment") {
		t.Fatalf("Load() error = %v, want private syntax error", err)
	}
}

func TestLoadUndefineClearsPrivateState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "private MODE := release\nundefine MODE\nMODE = debug\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MODE"]; got != "debug" {
		t.Fatalf("MODE = %q, want debug", got)
	}
	if len(project.Targets) != 1 || len(project.Targets[0].Commands) != 1 {
		t.Fatalf("targets = %#v, want one target with one command", project.Targets)
	}
	// Recipe text is stored verbatim; the resolved value is verified in the build package.
	if got := project.Targets[0].Commands[0].Text; got != "printf '%s' '$(MODE)' > out.txt" {
		t.Fatalf("command = %q, want verbatim recipe text", got)
	}
}

func TestLoadUndefineDirectiveRemovesVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE := release\nNAME = world\nundefine MODE NAME\nMODE_ORIGIN := $(origin MODE)\nNAME_FLAVOR := $(flavor NAME)\nMSG := $(MODE)-$(NAME)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := project.Variables["MODE"]; ok {
		t.Fatalf("MODE should be undefined, got %q", project.Variables["MODE"])
	}
	if _, ok := project.Variables["NAME"]; ok {
		t.Fatalf("NAME should be undefined, got %q", project.Variables["NAME"])
	}
	if got := project.Variables["MODE_ORIGIN"]; got != "undefined" {
		t.Fatalf("MODE_ORIGIN = %q, want undefined", got)
	}
	if got := project.Variables["NAME_FLAVOR"]; got != "undefined" {
		t.Fatalf("NAME_FLAVOR = %q, want undefined", got)
	}
	if got := project.Variables["MSG"]; got != "-" {
		t.Fatalf("MSG = %q, want -", got)
	}
}

func TestLoadUndefineDirectiveExpandsNameList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "TO_REMOVE = FOO BAR\nFOO = one\nBAR = two\nundefine $(TO_REMOVE)\nRESULT := $(FOO)$(BAR)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := project.Variables["FOO"]; ok {
		t.Fatalf("FOO should be undefined, got %q", project.Variables["FOO"])
	}
	if _, ok := project.Variables["BAR"]; ok {
		t.Fatalf("BAR should be undefined, got %q", project.Variables["BAR"])
	}
	if got := project.Variables["RESULT"]; got != "" {
		t.Fatalf("RESULT = %q, want empty", got)
	}
}

func TestLoadImportsEnvironmentVariables(t *testing.T) {
	t.Setenv("GOMAKE_ENV_IMPORT", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MSG := $(GOMAKE_ENV_IMPORT)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "from-env" {
		t.Fatalf("MSG = %q, want from-env", got)
	}
}

func TestLoadEnvironmentDoesNotOverrideMakefileByDefault(t *testing.T) {
	t.Setenv("GOMAKE_ENV_PRECEDENCE", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "GOMAKE_ENV_PRECEDENCE = from-file\nMSG := $(GOMAKE_ENV_PRECEDENCE)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "from-file" {
		t.Fatalf("MSG = %q, want from-file", got)
	}
}

func TestLoadEnvironmentOverridesMakefileWithContextFlag(t *testing.T) {
	t.Setenv("GOMAKE_ENV_PRECEDENCE", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "GOMAKE_ENV_PRECEDENCE = from-file\nMSG := $(GOMAKE_ENV_PRECEDENCE)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithContext(path, LoadContext{EnvironmentOverride: true})
	if err != nil {
		t.Fatalf("LoadWithContext() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "from-env" {
		t.Fatalf("MSG = %q, want from-env", got)
	}
}

func TestLoadOrderOnlyPrerequisites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all: input.txt | stamp\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := project.Targets[0]
	if len(target.Deps) != 1 || target.Deps[0] != "input.txt" {
		t.Fatalf("Deps = %v, want [input.txt]", target.Deps)
	}
	if len(target.OrderOnlyDeps) != 1 || target.OrderOnlyDeps[0] != "stamp" {
		t.Fatalf("OrderOnlyDeps = %v, want [stamp]", target.OrderOnlyDeps)
	}
}

func TestLoadEscapedSpacesInTargetAndPrerequisiteLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "NAME = out\\ file\nDEP = dep\\ file\nall: $(NAME)\n$(NAME): $(DEP)\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	if got := targets["all"].Deps; len(got) != 1 || got[0] != "out file" {
		t.Fatalf("all deps = %v, want [out file]", got)
	}
	if got := targets["out file"].Deps; len(got) != 1 || got[0] != "dep file" {
		t.Fatalf("out file deps = %v, want [dep file]", got)
	}
}

func TestLoadEscapedHashInTargetAndPrerequisiteLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "NAME = out\\#file\nDEP = dep\\#file\nall: $(NAME)\n$(NAME): $(DEP)\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	if got := targets["all"].Deps; len(got) != 1 || got[0] != "out#file" {
		t.Fatalf("all deps = %v, want [out#file]", got)
	}
	if got := targets["out#file"].Deps; len(got) != 1 || got[0] != "dep#file" {
		t.Fatalf("out#file deps = %v, want [dep#file]", got)
	}
}

func TestLoadVPathDirectiveTracksSearchPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	sep := string(os.PathListSeparator)
	content := fmt.Sprintf("vpath %%.txt src%[1]sassets\nvpath %%.cfg config extras\nall:\n\tprintf ok\n", sep)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.VPaths) != 2 {
		t.Fatalf("len(VPaths) = %d, want 2", len(project.VPaths))
	}
	if got := project.VPaths[0].Pattern; got != "%.txt" {
		t.Fatalf("VPaths[0].Pattern = %q, want %%.txt", got)
	}
	if dirs := project.VPaths[0].Directories; len(dirs) != 2 || dirs[0] != "src" || dirs[1] != "assets" {
		t.Fatalf("VPaths[0].Directories = %v, want [src assets]", dirs)
	}
	if got := project.VPaths[1].Pattern; got != "%.cfg" {
		t.Fatalf("VPaths[1].Pattern = %q, want %%.cfg", got)
	}
	if dirs := project.VPaths[1].Directories; len(dirs) != 2 || dirs[0] != "config" || dirs[1] != "extras" {
		t.Fatalf("VPaths[1].Directories = %v, want [config extras]", dirs)
	}
}

func TestLoadVPathDirectiveRemovesPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "vpath %.txt src\nvpath %.cfg config\nvpath %.txt\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.VPaths) != 1 {
		t.Fatalf("len(VPaths) = %d, want 1", len(project.VPaths))
	}
	if got := project.VPaths[0].Pattern; got != "%.cfg" {
		t.Fatalf("VPaths[0].Pattern = %q, want %%.cfg", got)
	}
	if dirs := project.VPaths[0].Directories; len(dirs) != 1 || dirs[0] != "config" {
		t.Fatalf("VPaths[0].Directories = %v, want [config]", dirs)
	}
}

func TestLoadVPathDirectiveClearsPatterns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "vpath %.txt src\nvpath\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.VPaths) != 0 {
		t.Fatalf("VPaths = %v, want empty", project.VPaths)
	}
}

func TestLoadPreservesConfiguredShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "SHELL = /bin/bash\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if project.Shell != "/bin/bash" {
		t.Fatalf("Shell = %q, want /bin/bash", project.Shell)
	}
}

func TestLoadPreservesConfiguredShellFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "SHELL = /bin/bash\n.SHELLFLAGS = -ec\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if project.ShellFlags != "-ec" {
		t.Fatalf("ShellFlags = %q, want -ec", project.ShellFlags)
	}
}

func TestLoadRejectsSpaceIndentedRecipeLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all:\n  printf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "missing separator") {
		t.Fatalf("Load() error = %v, want missing separator", err)
	}
}

func TestLoadInlineRecipeOnRuleLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "all: input.txt ; @printf '%s' '$<' > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Targets[0].Deps; len(got) != 1 || got[0] != "input.txt" {
		t.Fatalf("Deps = %v, want [input.txt]", got)
	}
	if len(project.Targets[0].Commands) != 1 {
		t.Fatalf("commands = %v, want 1 command", project.Targets[0].Commands)
	}
	if got := project.Targets[0].Commands[0].Text; got != "printf '%s' '$<' > out.txt" {
		t.Fatalf("command = %q, want inline recipe text", got)
	}
	if !project.Targets[0].Commands[0].Silent {
		t.Fatal("inline command should be silent")
	}
}

func TestLoadDefaultGoalFromVariable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".DEFAULT_GOAL := release\nall:\n\tprintf ok\nrelease:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if project.DefaultTarget != "release" {
		t.Fatalf("DefaultTarget = %q, want release", project.DefaultTarget)
	}
}

func TestLoadDefaultTargetSkipsDotSpecialTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".DEFAULT:\n\tprintf fallback\n.hidden:\n\tprintf hidden\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if project.DefaultTarget != "all" {
		t.Fatalf("DefaultTarget = %q, want all", project.DefaultTarget)
	}
}

func TestLoadRecipePrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".RECIPEPREFIX = >\nall:\n>@printf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.Targets[0].Commands) != 1 {
		t.Fatalf("commands = %v, want 1 command", project.Targets[0].Commands)
	}
	if got := project.Targets[0].Commands[0].Text; got != "printf ok" {
		t.Fatalf("command = %q, want printf ok", got)
	}
	if !project.Targets[0].Commands[0].Silent {
		t.Fatal("recipe-prefix command should be silent")
	}
}

func TestLoadSpecialSilentAndIgnoreTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".SILENT: quiet\n.IGNORE: tolerant\nquiet:\n\techo q\ntolerant:\n\tfalse\nall: quiet tolerant\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	if !targets["quiet"].Silent {
		t.Fatal("quiet target should be silent")
	}
	if targets["tolerant"].Silent {
		t.Fatal("tolerant target should not be silent")
	}
	if !targets["tolerant"].IgnoreErrors {
		t.Fatal("tolerant target should ignore errors")
	}
	if targets["quiet"].IgnoreErrors {
		t.Fatal("quiet target should not ignore errors")
	}
}

func TestLoadSpecialSilentAndIgnoreGlobal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".SILENT:\n.IGNORE:\nfirst:\n\techo one\nsecond:\n\tfalse\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, target := range project.Targets {
		if !target.Silent {
			t.Fatalf("target %s should be silent", target.Name)
		}
		if !target.IgnoreErrors {
			t.Fatalf("target %s should ignore errors", target.Name)
		}
	}
}

func TestLoadDeleteOnErrorTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".DELETE_ON_ERROR: out.txt\nout.txt:\n\tfalse\nother.txt:\n\tfalse\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	if !targets["out.txt"].DeleteOnError {
		t.Fatal("out.txt should have delete-on-error enabled")
	}
	if targets["other.txt"].DeleteOnError {
		t.Fatal("other.txt should not have delete-on-error enabled")
	}
}

func TestLoadDeleteOnErrorGlobal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".DELETE_ON_ERROR:\nout.txt:\n\tfalse\nother.txt:\n\tfalse\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, target := range project.Targets {
		if !target.DeleteOnError {
			t.Fatalf("target %s should have delete-on-error enabled", target.Name)
		}
	}
}

func TestLoadPreciousTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".PRECIOUS: keep.txt\nkeep.txt:\n\tfalse\nother.txt:\n\tfalse\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Name] = target
	}
	if !targets["keep.txt"].Precious {
		t.Fatal("keep.txt should be precious")
	}
	if targets["other.txt"].Precious {
		t.Fatal("other.txt should not be precious")
	}
}

func TestLoadPreciousGlobal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".PRECIOUS:\nout.txt:\n\tfalse\nother.txt:\n\tfalse\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, target := range project.Targets {
		if !target.Precious {
			t.Fatalf("target %s should be precious", target.Name)
		}
	}
}

func TestLoadExportDirectiveMarksVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = release\nexport MODE\nexport EXTRA = value\nunexport MODE\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !project.ExportedVariables["EXTRA"] {
		t.Fatal("EXTRA should be exported")
	}
	if project.ExportedVariables["MODE"] {
		t.Fatal("MODE should be unexported")
	}
	if !project.UnexportedVariables["MODE"] {
		t.Fatal("MODE should be marked unexported")
	}
	if got := project.Variables["EXTRA"]; got != "value" {
		t.Fatalf("EXTRA = %q, want value", got)
	}
}

func TestLoadExportAllVariablesDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := ".EXPORT_ALL_VARIABLES:\nMODE = release\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !project.ExportAllVariables {
		t.Fatal("ExportAllVariables = false, want true")
	}
}

func TestLoadSupportsBuiltinTextAndListFunctions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "WORDS = b a b c\n" +
		"SUBST := $(subst b,B,$(WORDS))\n" +
		"STRIP := $(strip    b   a   c   )\n" +
		"FIND := $(findstring a,$(WORDS))\n" +
		"FILTER := $(filter a c,$(WORDS))\n" +
		"FILTEROUT := $(filter-out b,$(WORDS))\n" +
		"SORTED := $(sort $(WORDS))\n" +
		"WORD2 := $(word 2,$(WORDS))\n" +
		"WORDLIST := $(wordlist 2,3,$(WORDS))\n" +
		"WORDSCOUNT := $(words $(WORDS))\n" +
		"FIRST := $(firstword $(WORDS))\n" +
		"LAST := $(lastword $(WORDS))\n" +
		"JOINED := $(join a b,c d)\n" +
		"all:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := map[string]string{
		"SUBST":      "B a B c",
		"STRIP":      "b a c",
		"FIND":       "a",
		"FILTER":     "a c",
		"FILTEROUT":  "a c",
		"SORTED":     "a b c",
		"WORD2":      "a",
		"WORDLIST":   "a b",
		"WORDSCOUNT": "4",
		"FIRST":      "b",
		"LAST":       "c",
		"JOINED":     "ac bd",
	}
	for key, want := range checks {
		if got := project.Variables[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadSupportsBuiltinPathAndConditionalFunctions(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(realFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}
	path := filepath.Join(dir, "Makefile")
	content := fmt.Sprintf("FILES = src/main.go lib/util.c README\nDIRS := $(dir $(FILES))\nNOTDIR := $(notdir $(FILES))\nBASE := $(basename $(FILES))\nSUFFIX := $(suffix $(FILES))\nADDPREFIX := $(addprefix out/,$(notdir $(FILES)))\nADDSUFFIX := $(addsuffix .bak,$(notdir $(FILES)))\nCOND := $(if $(FILES),yes,no)\nORVAL := $(or , ,first,second)\nANDVAL := $(and one,two,three)\nRAW = $(FILES)\nVAL := $(value RAW)\nFLAVOR_RAW := $(flavor RAW)\nFLAVOR_UNDEF := $(flavor MISSING)\nABS := $(abspath %s)\nREAL := $(realpath %s)\nBRACE_FUNC := ${strip   brace   value   }\nall:\n\tprintf ok\n", realFile, realFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := map[string]string{
		"DIRS":         "src/ lib/ ./",
		"NOTDIR":       "main.go util.c README",
		"BASE":         "src/main lib/util README",
		"SUFFIX":       ".go .c",
		"ADDPREFIX":    "out/main.go out/util.c out/README",
		"ADDSUFFIX":    "main.go.bak util.c.bak README.bak",
		"COND":         "yes",
		"ORVAL":        "first",
		"ANDVAL":       "three",
		"VAL":          "$(FILES)",
		"FLAVOR_RAW":   "recursive",
		"FLAVOR_UNDEF": "undefined",
		"ABS":          realFile,
		"REAL":         realFile,
		"BRACE_FUNC":   "brace value",
	}
	for key, want := range checks {
		if got := project.Variables[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadSupportsConditionalSpellingsWithoutSpace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MODE = debug\nifeq($(MODE),debug)\nMSG = yes\nendif\nifdef\tMODE\nHAS_MODE = yes\nendif\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := project.Variables["MSG"]; got != "yes" {
		t.Fatalf("MSG = %q, want yes", got)
	}
	if got := project.Variables["HAS_MODE"]; got != "yes" {
		t.Fatalf("HAS_MODE = %q, want yes", got)
	}
}

func TestLoadSupportsAdditionalBuiltinFunctions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"src/a.c", "src/b.c", "src/x.h"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	path := filepath.Join(dir, "Makefile")
	content := "LIST = src/a.c src/b.c src/x.h\n" +
		"PATSUB := $(patsubst %.c,%.o,$(LIST))\n" +
		"WILDCARD := $(wildcard src/*.c src/*.h)\n" +
		"TEMPLATE = hi-$(1)-$(2)\n" +
		"CALLVAL := $(call TEMPLATE,one,two)\n" +
		"FOREACH := $(foreach v,a b,$(v)-x)\n" +
		"ORIGIN_LIST := $(origin LIST)\n" +
		"ORIGIN_CURDIR := $(origin CURDIR)\n" +
		"ORIGIN_MISSING := $(origin MISSING)\n" +
		"SHELLOUT := $(shell printf 'alpha\\nbeta\\n')\n" +
		"WRITE1 := $(file > generated.txt,from-file)\n" +
		"WRITE2 := $(file >> generated.txt,again)\n" +
		"FILEREAD := $(file < generated.txt)\n" +
		"TRIGGER := $(eval TEMP := yes)\n" +
		"all:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := map[string]string{
		"PATSUB":         "src/a.o src/b.o src/x.h",
		"WILDCARD":       "src/a.c src/b.c src/x.h",
		"CALLVAL":        "hi-one-two",
		"FOREACH":        "a-x b-x",
		"ORIGIN_LIST":    "file",
		"ORIGIN_CURDIR":  "default",
		"ORIGIN_MISSING": "undefined",
		"SHELLOUT":       "alpha beta",
		"FILEREAD":       "from-file again",
		"TEMP":           "yes",
	}
	for key, want := range checks {
		if got := project.Variables[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadOriginFunctionReportsVariableSources(t *testing.T) {
	t.Setenv("GOMAKE_ORIGIN_ENV", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "FILE_VAR = from-file\noverride OVERRIDE_VAR = forced\nENV_ORIGIN := $(origin GOMAKE_ORIGIN_ENV)\nFILE_ORIGIN := $(origin FILE_VAR)\nDEFAULT_ORIGIN := $(origin CURDIR)\nMISSING_ORIGIN := $(origin MISSING)\nOVERRIDE_ORIGIN := $(origin MODE)\nOVERRIDE_VAR_ORIGIN := $(origin OVERRIDE_VAR)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := LoadWithOverrides(path, map[string]string{"MODE": "release"})
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}

	checks := map[string]string{
		"ENV_ORIGIN":          "environment",
		"FILE_ORIGIN":         "file",
		"DEFAULT_ORIGIN":      "default",
		"MISSING_ORIGIN":      "undefined",
		"OVERRIDE_ORIGIN":     "command line",
		"OVERRIDE_VAR_ORIGIN": "override",
	}
	for key, want := range checks {
		if got := project.Variables[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadWarnUndefinedVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "MSG := $(MISSING_VAR)\nall:\n\tprintf ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = stderr
	})

	_, err = LoadWithContext(path, LoadContext{WarnUndefined: true})
	if err != nil {
		t.Fatalf("LoadWithContext() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !strings.Contains(string(output), "warning: undefined variable MISSING_VAR") {
		t.Fatalf("stderr = %q, want undefined-variable warning", string(output))
	}
}
