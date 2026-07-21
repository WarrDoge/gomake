//go:build integration

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIBuildSampleVerify(t *testing.T) {
	bin := buildTestBinary(t)
	root := repoRoot(t)

	cmd := exec.Command(bin, "-f", "./examples/sample", "verify")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(root, "examples", "sample", "build", "demo.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "hello from go" {
		t.Fatalf("sample output = %q", content)
	}
}

func TestCLIVersion(t *testing.T) {
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "--version")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "gomake 0.3.0" {
		t.Fatalf("version output = %q", output)
	}
}

func TestCLIHelp(t *testing.T) {
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "--help")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "usage: gomake [options] [targets...]" {
		t.Fatalf("help output = %q", output)
	}
}

func TestCLIRespectsDashPrefix(t *testing.T) {
	bin := buildTestBinary(t)
	root := repoRoot(t)
	dir := filepath.Join(root, ".tmp", "integration-ignore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all:\n\t-false\n\tprintf 'ok' > out.txt\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin, "-f", dir)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Fatalf("Stat(out.txt) error = %v", err)
	}
}

func TestCLIOneShell(t *testing.T) {
	bin := buildTestBinary(t)
	root := repoRoot(t)
	dir := filepath.Join(root, ".tmp", "integration-oneshell")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(".ONESHELL:\nall:\n\tfoo=bar\n\tprintf '%s' \"$$foo\" > out.txt\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin, "-f", dir)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "bar" {
		t.Fatalf("out.txt = %q, want bar", content)
	}
}

func TestCLIPatternRules(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
all: foo.o bar.o
	@echo "Build complete!"

%.o: %.c
	@echo "Rule for %.o: %.c"
	cc -c $< -o $@

%.c: %.y
	@echo "Rule for %.c: %.y"
	echo "int main() { return 0; }" > $@

foo.y:
	@echo "Rule for foo.y"
	touch foo.y

# bar.c is explicit, but built via pattern rule if we don't provide commands
bar.c:
	@echo "Rule for bar.c"
	echo "int main() { return 1; }" > bar.c
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	out := string(output)
	if !strings.Contains(out, "Rule for foo.y") {
		t.Errorf("output missing 'Rule for foo.y':\n%s", out)
	}
	if !strings.Contains(out, "Rule for %.c: %.y") {
		t.Errorf("output missing 'Rule for %%.c: %%.y':\n%s", out)
	}
	if !strings.Contains(out, "Rule for %.o: %.c") {
		t.Errorf("output missing 'Rule for %%.o: %%.c':\n%s", out)
	}
	if !strings.Contains(out, "Rule for bar.c") {
		t.Errorf("output missing 'Rule for bar.c':\n%s", out)
	}
	if !strings.Contains(out, "rm foo.c") {
		t.Errorf("output missing 'rm foo.c' (intermediate cleanup):\n%s", out)
	}
	if strings.Contains(out, "rm bar.c") {
		t.Errorf("output contains 'rm bar.c' (bar.c should not be intermediate):\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(dir, "foo.o")); err != nil {
		t.Errorf("Stat(foo.o) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bar.o")); err != nil {
		t.Errorf("Stat(bar.o) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "foo.c")); err == nil {
		t.Errorf("foo.c should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "bar.c")); err != nil {
		t.Errorf("bar.c should have been preserved")
	}
}

func TestCLIDoubleColonRules(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
.PHONY: test-double
all: test-double
	@echo "All done"

test-double::
	@echo "First double-colon rule"

test-double::
	@echo "Second double-colon rule"
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// 1. Initial build
	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("initial build failed: %v\n%s", err, output)
	}
	out := string(output)
	if !strings.Contains(out, "First double-colon rule") || !strings.Contains(out, "Second double-colon rule") {
		t.Errorf("initial build should run both rules:\n%s", out)
	}
}

func TestCLIGroupedTargets(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
all: foo bar
	@echo "All done"

foo bar &: prereq
	@echo "Grouped rule executed"
	touch foo bar

prereq:
	touch prereq

clean:
	rm -f foo bar prereq
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	out := string(output)
	count := strings.Count(out, "Grouped rule executed")
	if count != 1 {
		t.Errorf("expected 'Grouped rule executed' to appear exactly once, got %d times\n%s", count, out)
	}
}

func TestCLIArchiveMemberTargets(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
all: test.a(foo.o)
	@echo "All done"

test.a(foo.o): foo.o
	@echo "Archiving foo.o"
	ar Ucr test.a foo.o

foo.o: foo.c
	cc -c foo.c -o foo.o

foo.c:
	echo "int main() { return 0; }" > foo.c
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// 1. Initial build
	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("initial build failed: %v\n%s", err, output)
	}
	out := string(output)
	if !strings.Contains(out, "Archiving foo.o") {
		t.Errorf("initial build should run archiving:\n%s", out)
	}

	// 2. Second build should be a no-op
	cmd = exec.Command(bin)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("second build failed: %v\n%s", err, output)
	}
	out = string(output)
	if strings.Contains(out, "Archiving foo.o") {
		t.Errorf("second build should NOT run archiving:\n%s", out)
	}

	// 3. Touch source, rebuild should archive
	time.Sleep(1100 * time.Millisecond) // Ensure timestamp change
	if err := os.WriteFile(filepath.Join(dir, "foo.c"), []byte("int main() { return 1; }"), 0o644); err != nil {
		t.Fatalf("failed to update foo.c: %v", err)
	}

	cmd = exec.Command(bin)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("third build failed: %v\n%s", err, output)
	}
	out = string(output)
	if !strings.Contains(out, "Archiving foo.o") {
		t.Errorf("third build should run archiving after source change:\n%s", out)
	}
}

func TestCLISuffixRules(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
.SUFFIXES: .c .o

.c.o:
	@echo "Compiling $< to $@"
	cc -c $< -o $@

all: foo.o
	@echo "All done"

foo.c:
	echo "int main() { return 0; }" > foo.c

clean:
	rm -f foo.c foo.o
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	out := string(output)
	if !strings.Contains(out, "Compiling foo.c to foo.o") {
		t.Errorf("output missing 'Compiling foo.c to foo.o':\n%s", out)
	}
}

func TestCLISpecialTargets(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
all: final1 final2
	@echo "All done"

final1: int1
	touch final1

int1:
	touch int1

final2: int2
	touch final2

int2:
	touch int2

.INTERMEDIATE: int1
.SECONDARY: int2
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
	out := string(output)

	if !strings.Contains(out, "rm int1") {
		t.Errorf("expected 'rm int1' for intermediate target, got:\n%s", out)
	}
	if strings.Contains(out, "rm int2") || strings.Contains(out, "rm int1 int2") {
		t.Errorf("did not expect 'rm int2' because it is secondary, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "int1")); err == nil {
		t.Errorf("int1 should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "int2")); err != nil {
		t.Errorf("int2 should have been preserved")
	}
}

func TestCLIDefaultRule(t *testing.T) {
	bin := buildTestBinary(t)
	dir := t.TempDir()

	makefile := `
all: missing-prereq
	@echo "All done"

.DEFAULT:
	@echo "Fallback for $@"
	touch $@
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	out := string(output)
	if !strings.Contains(out, "Fallback for missing-prereq") {
		t.Errorf("output missing fallback message:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-prereq")); err != nil {
		t.Errorf("missing-prereq should have been created")
	}
}

func TestCLIParallelJobsBuildIndependentTargets(t *testing.T) {
	files := map[string]string{
		"Makefile": ".PHONY: all first second\nall: first second\nfirst:\n\tsleep 2; printf 'first' > first.txt\nsecond:\n\tsleep 2; printf 'second' > second.txt\n",
	}

	started := time.Now()
	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-j", "2", "all")
	elapsed := time.Since(started)
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if elapsed >= 3800*time.Millisecond {
		t.Fatalf("elapsed = %v, want < 3.8s for -j parallel execution", elapsed)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "first.txt")); got != "first" {
		t.Fatalf("first.txt = %q, want first", got)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "second.txt")); got != "second" {
		t.Fatalf("second.txt = %q, want second", got)
	}
}

func TestDifferentialGNUIncludeConditionalsAndOrderOnly(t *testing.T) {
	makefile := "" +
		"include config.mk\n" +
		".PHONY: all prep\n" +
		"MODE ?= debug\n" +
		"ifeq ($(MODE),debug)\n" +
		"MSG = debug\n" +
		"else\n" +
		"MSG = release\n" +
		"endif\n" +
		"prep:\n" +
		"\tprintf 'ready' > prep.stamp\n" +
		"all: input.txt | prep\n" +
		"\tprintf '%s %s %s\\n' '$(MSG)' '$(EXTRA)' '$|' > out.txt\n"
	files := map[string]string{
		"Makefile":  makefile,
		"config.mk": "EXTRA = included\n",
		"input.txt": "input\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOutput := normalizeRunOutput(makeResult.output, makeResult.dir)
	gomakeOutput := normalizeRunOutput(gomakeResult.output, gomakeResult.dir)
	if makeOutput != gomakeOutput {
		t.Fatalf("output mismatch:\nmake:\n%s\ngomake:\n%s", makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLICommandLineVariableOverride(t *testing.T) {
	files := map[string]string{
		"Makefile": "MODE = debug\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "MODE=release", "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "MODE=release", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIBangEqualsAssignment(t *testing.T) {
	files := map[string]string{
		"Makefile": "NAME = world\nMSG != printf 'hello $(NAME)\\nnext\\n'\nNAME = there\nall:\n\tprintf '%s|%s' '$(flavor MSG)' '$(MSG)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIDoubleColonImmediateAssignment(t *testing.T) {
	files := map[string]string{
		"Makefile": "NAME = world\nMSG ::= hello $(NAME)\nNAME = there\nall:\n\tprintf '%s' '$(MSG)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLISingleCharacterVariableReferences(t *testing.T) {
	files := map[string]string{
		"Makefile": "X = make\nall:\n\tX=shell; printf '%s|%s' '$X' \"$${X}\" > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIValueFunctionPreservesUnexpandedRecursiveText(t *testing.T) {
	files := map[string]string{
		"Makefile": "X = one\nY = $(X)\nV := $(value Y)\nall:\n\tprintf '%s' '$(V)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLITopLevelEvalAssignmentLine(t *testing.T) {
	files := map[string]string{
		"Makefile": "$(eval X := hi)\nall:\n\tprintf '%s' '$(X)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLITopLevelEvalAssignmentWithInlineHash(t *testing.T) {
	files := map[string]string{
		"Makefile": "$(eval X := hi #comment)\nall:\n\tprintf '%s' '$(X)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLICallGeneratedEvalAssignmentLine(t *testing.T) {
	files := map[string]string{
		"Makefile": "GEN = Z := $(1)\n$(eval $(call GEN,ok))\nall:\n\tprintf '%s' '$(Z)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIAssignmentParsingWithNestedAssignmentSyntax(t *testing.T) {
	files := map[string]string{
		"Makefile": "DEF = Y := $(X)\nX = hi\n$(eval $(DEF))\nall:\n\tprintf '%s' '$(Y)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIEvalGeneratedRuleFromDefine(t *testing.T) {
	files := map[string]string{
		"Makefile": "define RULE\ndynamic:\n\tprintf 'generated' > dynamic.txt\nendef\n$(eval $(RULE))\nall: dynamic\n\t@:\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "dynamic.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "dynamic.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIEvalGeneratedInlineRule(t *testing.T) {
	files := map[string]string{
		"Makefile": "$(eval generated: ; printf 'ok' > out.txt)\nall: generated\n\t@:\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIEscapedSpacesInTargetAndPrerequisiteLists(t *testing.T) {
	files := map[string]string{
		"Makefile": ".PHONY: all\nNAME = out\\ file\nDEP = dep\\ file\nall: $(NAME)\n$(DEP):\n\tprintf 'dep' > \"$@\"\n$(NAME): $(DEP)\n\tprintf 'ok' > \"$@\"\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"dep file", "out file"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLIEscapedHashInTargetAndPrerequisiteLists(t *testing.T) {
	files := map[string]string{
		"Makefile": ".PHONY: all\nNAME = out\\#file\nDEP = dep\\#file\nall: $(NAME)\n$(DEP):\n\tprintf 'dep' > \"$@\"\n$(NAME): $(DEP)\n\tprintf 'ok' > \"$@\"\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"dep#file", "out#file"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLIRecursiveVariableErrorsWhenExpanded(t *testing.T) {
	// Known limitation (see TODO.md): gomake resolves recursive globals once at
	// load time, flattening a self-reference like `A = $(A) x` to empty instead
	// of aborting when it is later expanded, as GNU make does.
	t.Skip("self-referential recursive variable detection not implemented")

	files := map[string]string{
		"Makefile": "A = $(A) x\nall:\n\tprintf '%s' '$(A)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode == 0 || gomakeResult.exitCode == 0 {
		t.Fatalf("both make and gomake should fail\nmake=%d\ngomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
}

func TestCLIUnusedRecursiveVariableDoesNotFailBuild(t *testing.T) {
	files := map[string]string{
		"Makefile": "A = $(A) x\nall:\n\tprintf '%s' 'ok' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLITargetSpecificVariableAssignment(t *testing.T) {
	files := map[string]string{
		"Makefile": "MODE = global\napp: MODE = local\napp:\n\tprintf '%s' '$(MODE)' > app.txt\nall:\n\tprintf '%s' '$(MODE)' > all.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all", "app")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all", "app")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"all.txt", "app.txt"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLITargetAndPatternSpecificCommandLinePrecedenceAndOverride(t *testing.T) {
	files := map[string]string{
		"Makefile": "app: MODE = target\noverride-mode: override MODE = target-override\n%.txt: MODE = pattern\n%.cfg: override MODE = pattern-override\napp:\n\tprintf '%s' '$(MODE)' > app.out\noverride-mode:\n\tprintf '%s' '$(MODE)' > override.out\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\none.cfg:\n\tprintf '%s' '$(MODE)' > one.cfg\n",
	}

	makeResult := runMakeLike(t, "make", files, "MODE=cli", "app", "override-mode", "one.txt", "one.cfg")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "MODE=cli", "app", "override-mode", "one.txt", "one.cfg")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"app.out", "override.out", "one.txt", "one.cfg"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLICommandLineAppendSemanticsForTargetSpecificOverrides(t *testing.T) {
	files := map[string]string{
		"Makefile": "plain: CFLAGS += -g\nplain:\n\tprintf '%s' '$(CFLAGS)' > plain.out\noverride: override CFLAGS += -g\noverride:\n\tprintf '%s' '$(CFLAGS)' > override.out\n",
	}

	makeResult := runMakeLike(t, "make", files, "CFLAGS=-O2", "plain", "override")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "CFLAGS=-O2", "plain", "override")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"plain.out", "override.out"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLIPatternSpecificAppendWithCommandLineOverride(t *testing.T) {
	files := map[string]string{
		"Makefile": "%.txt: CFLAGS += -g\none.txt:\n\tprintf '%s|%s' '$(value CFLAGS)' '$(CFLAGS)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "CFLAGS=-O2", "one.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "CFLAGS=-O2", "one.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIPatternSpecificMultipleAppendsWithCommandLineOverride(t *testing.T) {
	files := map[string]string{
		"Makefile": "%.txt: CFLAGS += A\n%.txt: CFLAGS += B\none.txt:\n\tprintf '%s|%s' '$(value CFLAGS)' '$(CFLAGS)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "CFLAGS=cli", "one.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "CFLAGS=cli", "one.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIPatternSpecificAppendThenOverrideAppendWithCommandLineOverride(t *testing.T) {
	files := map[string]string{
		"Makefile": "%.txt: CFLAGS += A\n%.txt: override CFLAGS += B\none.txt:\n\tprintf '%s|%s|%s' '$(value CFLAGS)' '$(origin CFLAGS)' '$(CFLAGS)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "CFLAGS=cli", "one.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "CFLAGS=cli", "one.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLITargetAndPatternOverrideOriginFunction(t *testing.T) {
	files := map[string]string{
		"Makefile": "target: override MODE = target-override\n%.txt: override MODE = pattern-override\ntarget:\n\tprintf '%s|%s' '$(MODE)' '$(origin MODE)' > target.out\none.txt:\n\tprintf '%s|%s' '$(MODE)' '$(origin MODE)' > one.out\n",
	}

	makeResult := runMakeLike(t, "make", files, "MODE=cli", "target", "one.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "MODE=cli", "target", "one.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, pair := range [][2]string{{"target.out", "target"}, {"one.out", "one"}} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, pair[0]))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, pair[0]))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", pair[0], makeOut, gomakeOut)
		}
	}
}

func TestCLIMixedTargetSpecificAndPatternOverrideCommandLinePrecedence(t *testing.T) {
	files := map[string]string{
		"Makefile": "%.txt: override MODE = pattern\none.txt: MODE = target\none.txt:\n\tprintf '%s' '$(MODE)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "MODE=cli", "one.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "MODE=cli", "one.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIPatternSpecificVariableAssignment(t *testing.T) {
	files := map[string]string{
		"Makefile": "MODE = global\n%.txt: MODE = pattern\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\none.bin:\n\tprintf '%s' '$(MODE)' > one.bin\n",
	}

	makeResult := runMakeLike(t, "make", files, "one.txt", "one.bin")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "one.txt", "one.bin")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	for _, name := range []string{"one.txt", "one.bin"} {
		makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, name))
		gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, name))
		if makeOut != gomakeOut {
			t.Fatalf("artifact %s mismatch: make=%q gomake=%q", name, makeOut, gomakeOut)
		}
	}
}

func TestCLIPatternSpecificVariablePrecedence(t *testing.T) {
	files := map[string]string{
		"Makefile": "special%.txt: MODE = specific\n%.txt: MODE = generic\nsp%1.txt: TIE = first\nspe%.txt: TIE = second\nspecial1.txt:\n\tprintf '%s|%s' '$(MODE)' '$(TIE)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "special1.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "special1.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIDefaultRuleBuildsUnknownTarget(t *testing.T) {
	files := map[string]string{
		"Makefile": ".DEFAULT:\n\tprintf 'default:%s' '$@' > $@\nall:\n\t@:\n",
	}

	makeResult := runMakeLike(t, "make", files, "generated.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "generated.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "generated.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "generated.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIDefaultRuleBuildsMissingPrerequisite(t *testing.T) {
	files := map[string]string{
		"Makefile": ".DEFAULT:\n\tprintf 'dep' > $@\nout.txt: missing.txt\n\tcat $< > $@\n",
	}

	makeResult := runMakeLike(t, "make", files, "out.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "out.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIDeleteOnErrorRemovesFailedTarget(t *testing.T) {
	files := map[string]string{
		"Makefile": ".DELETE_ON_ERROR:\nout.txt:\n\tprintf 'partial' > out.txt; false\n",
	}

	makeResult := runMakeLike(t, "make", files, "out.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "out.txt")

	if makeResult.exitCode == 0 || gomakeResult.exitCode == 0 {
		t.Fatalf("both make and gomake should fail\nmake=%d\ngomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIPreciousTargetSkipsDeleteOnErrorCleanup(t *testing.T) {
	files := map[string]string{
		"Makefile": ".DELETE_ON_ERROR:\n.PRECIOUS: out.txt\nout.txt:\n\tprintf 'partial' > out.txt; false\n",
	}

	makeResult := runMakeLike(t, "make", files, "out.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "out.txt")

	if makeResult.exitCode == 0 || gomakeResult.exitCode == 0 {
		t.Fatalf("both make and gomake should fail\nmake=%d\ngomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIBuiltinInvocationVariables(t *testing.T) {
	files := map[string]string{
		"Makefile": "" +
			"include config.mk\n" +
			".PHONY: test\n" +
			"all:\n" +
			"\tprintf '%s\\n%s\\n%s\\n' '$(MAKE)' '$(MAKECMDGOALS)' '$(MAKEFILE_LIST)' > out.txt\n" +
			"test:\n" +
			"\t@:\n",
		"config.mk": "MSG = included\n",
	}

	makeResult := runMakeLike(t, "make", files, "all", "test")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all", "test")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}

	makeOut := normalizeRunOutput(readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt")), makeResult.dir)
	gomakeOut := normalizeRunOutput(readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt")), gomakeResult.dir)
	makeLines := strings.Split(strings.TrimSpace(makeOut), "\n")
	gomakeLines := strings.Split(strings.TrimSpace(gomakeOut), "\n")
	if len(makeLines) != 3 || len(gomakeLines) != 3 {
		t.Fatalf("unexpected output shape: make=%q gomake=%q", makeOut, gomakeOut)
	}
	if makeLines[1] != gomakeLines[1] || makeLines[2] != gomakeLines[2] {
		t.Fatalf("builtin output mismatch:\nmake:\n%s\ngomake:\n%s", makeOut, gomakeOut)
	}
	if gomakeLines[0] != "gomake" {
		t.Fatalf("MAKE = %q, want gomake", gomakeLines[0])
	}
}

func TestCLIDryRunRunsOnlyForcedCommands(t *testing.T) {
	files := map[string]string{
		"Makefile": ".PHONY: all\nall:\n\tprintf 'normal' > normal.txt\n\t+printf 'forced' > forced.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-n", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "normal.txt")); got != "" {
		t.Fatalf("normal.txt = %q, want empty", got)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "forced.txt")); got != "forced" {
		t.Fatalf("forced.txt = %q, want forced", got)
	}
}

func TestCLIDryRunRunsRecursiveMakeCommands(t *testing.T) {
	files := map[string]string{
		"Makefile": "MAKE = printf 'recursive' > recursive.txt\n.PHONY: all\nall:\n\t$(MAKE)\n\tprintf 'normal' > normal.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-n", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "recursive.txt")); got != "recursive" {
		t.Fatalf("recursive.txt = %q, want recursive", got)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "normal.txt")); got != "" {
		t.Fatalf("normal.txt = %q, want empty", got)
	}
}

func TestCLIKeepGoingBuildsLaterTargets(t *testing.T) {
	files := map[string]string{
		"Makefile": ".PHONY: fail ok\nfail:\n\tfalse\nok:\n\tprintf 'ok' > ok.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-k", "fail", "ok")
	if result.exitCode == 0 {
		t.Fatalf("gomake exited with %d, want non-zero\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "ok.txt")); got != "ok" {
		t.Fatalf("ok.txt = %q, want ok", got)
	}
}

func TestCLITouchCreatesTargetsWithoutExecutingRecipes(t *testing.T) {
	files := map[string]string{
		"Makefile":  "out.txt: input.txt\n\tfalse\n",
		"input.txt": "input\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-t", "out.txt")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d\n%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(filepath.Join(result.dir, "out.txt")); err != nil {
		t.Fatalf("Stat(out.txt) error = %v", err)
	}
}

func TestCLIQuestionModeReturnsNonZeroForOutOfDateTarget(t *testing.T) {
	files := map[string]string{
		"Makefile": "out.txt:\n\tprintf 'built' > out.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-q", "out.txt")
	if result.exitCode != 1 {
		t.Fatalf("gomake exited with %d, want 1\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "" {
		t.Fatalf("out.txt = %q, want empty", got)
	}
}

func TestCLIQuestionModeReturnsZeroForUpToDateTarget(t *testing.T) {
	files := map[string]string{
		"Makefile": "out.txt:\n\tprintf 'built' > out.txt\n",
		"out.txt":  "existing\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-q", "out.txt")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "existing\n" {
		t.Fatalf("out.txt = %q, want existing", got)
	}
}

func TestCLIWhatIfForcesTargetRebuild(t *testing.T) {
	files := map[string]string{
		"Makefile": "out.txt:\n\tprintf 'rebuilt' > out.txt\n",
		"out.txt":  "existing\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-W", "out.txt", "out.txt")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "rebuilt" {
		t.Fatalf("out.txt = %q, want rebuilt", got)
	}
}

func TestCLIEnvironmentOverrideFlag(t *testing.T) {
	t.Setenv("MODE", "from-env")
	files := map[string]string{
		"Makefile": "MODE = from-file\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n",
	}

	withoutOverride := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	withOverride := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-e", "all")

	if got := readFileOrEmpty(t, filepath.Join(withoutOverride.dir, "out.txt")); got != "from-file" {
		t.Fatalf("without -e out.txt = %q, want from-file", got)
	}
	if got := readFileOrEmpty(t, filepath.Join(withOverride.dir, "out.txt")); got != "from-env" {
		t.Fatalf("with -e out.txt = %q, want from-env", got)
	}
}

func TestCLIPrintDatabaseFlag(t *testing.T) {
	files := map[string]string{
		"Makefile": "FOO = bar\n.PHONY: all\nall:\n\t@:\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-p", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if !strings.Contains(result.output, "# Variables") {
		t.Fatalf("output = %q, want variables banner", result.output)
	}
	if !strings.Contains(result.output, "FOO = bar") {
		t.Fatalf("output = %q, want FOO variable", result.output)
	}
	if !strings.Contains(result.output, "# Targets") {
		t.Fatalf("output = %q, want targets banner", result.output)
	}
	if !strings.Contains(result.output, "all:") {
		t.Fatalf("output = %q, want all target", result.output)
	}
}

func TestCLIWarnUndefinedVariablesFlag(t *testing.T) {
	files := map[string]string{
		"Makefile": "MSG := $(MISSING_VAR)\n.PHONY: all\nall:\n\t@:\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "--warn-undefined-variables", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if !strings.Contains(result.output, "warning: undefined variable MISSING_VAR") {
		t.Fatalf("output = %q, want undefined-variable warning", result.output)
	}
}

func TestCLIMakeflagsBuiltinVariable(t *testing.T) {
	files := map[string]string{
		"Makefile": "all:\n\tprintf '%s' \"$$MAKEFLAGS\" > out.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "-k", "--warn-undefined-variables", "MODE=release", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	flags := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt"))
	if !strings.Contains(flags, "-k") {
		t.Fatalf("MAKEFLAGS = %q, want -k", flags)
	}
	if !strings.Contains(flags, "--warn-undefined-variables") {
		t.Fatalf("MAKEFLAGS = %q, want warn-undefined flag", flags)
	}
	if !strings.Contains(flags, "MODE=release") {
		t.Fatalf("MAKEFLAGS = %q, want command-line override", flags)
	}
}

func TestCLIMakelevelBuiltinAndEnvironment(t *testing.T) {
	files := map[string]string{
		"Makefile": "all:\n\tprintf '%s|%s' '$(MAKELEVEL)' \"$$MAKELEVEL\" > out.txt\n",
	}

	defaultLevel := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	if defaultLevel.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", defaultLevel.exitCode, defaultLevel.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(defaultLevel.dir, "out.txt")); got != "0|0" {
		t.Fatalf("default MAKELEVEL = %q, want 0|0", got)
	}

	t.Setenv("MAKELEVEL", "2")
	inheritedLevel := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	if inheritedLevel.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", inheritedLevel.exitCode, inheritedLevel.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(inheritedLevel.dir, "out.txt")); got != "3|3" {
		t.Fatalf("inherited MAKELEVEL = %q, want 3|3", got)
	}
}

func TestCLISpecialSilentAndIgnore(t *testing.T) {
	files := map[string]string{
		"Makefile": ".SILENT: all\n.IGNORE: all\nall:\n\tfalse\n\tprintf 'ok' > out.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "ok" {
		t.Fatalf("out.txt = %q, want ok", got)
	}
	if strings.Contains(result.output, "printf 'ok' > out.txt") {
		t.Fatalf("output = %q, want no echoed commands", result.output)
	}
}

func TestCLIExportAndUnexportDirectives(t *testing.T) {
	files := map[string]string{
		"Makefile": "MODE = from-file\nexport MODE\nunexport MODE\nall:\n\tprintf '%s' \"$$MODE\" > out.txt\n",
	}
	t.Setenv("MODE", "from-env")

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "" {
		t.Fatalf("out.txt = %q, want empty (unexported)", got)
	}
}

func TestCLIExportAllVariablesDirective(t *testing.T) {
	files := map[string]string{
		"Makefile": ".EXPORT_ALL_VARIABLES:\nMODE = from-file\nall:\n\tprintf '%s' \"$$MODE\" > out.txt\n",
	}

	result := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "out.txt")); got != "from-file" {
		t.Fatalf("out.txt = %q, want from-file", got)
	}
}

func TestCLIRecursiveMakePropagatesDryRunViaMakeflags(t *testing.T) {
	bin := buildTestBinary(t)
	files := map[string]string{
		"Makefile": ".PHONY: all child\nall:\n\t$(MAKE) -f . child\nchild:\n\tprintf 'child' > child.txt\n",
	}

	result := runMakeLike(t, bin, files, "-f", ".", "-n", "MAKE="+bin, "all")
	if result.exitCode != 0 {
		t.Fatalf("gomake exited with %d, want 0\n%s", result.exitCode, result.output)
	}
	if got := readFileOrEmpty(t, filepath.Join(result.dir, "child.txt")); got != "" {
		t.Fatalf("child.txt = %q, want empty (dry-run propagated)", got)
	}
}

func TestCLIVPathResolvesPrerequisites(t *testing.T) {
	files := map[string]string{
		"Makefile":      "vpath %.txt src\nout.txt: input.txt\n\tprintf '%s|%s' '$<' '$^' > out.txt\n",
		"src/input.txt": "input\n",
	}

	makeResult := runMakeLike(t, "make", files, "out.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "out.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIVPATHVariableResolvesPrerequisites(t *testing.T) {
	files := map[string]string{
		"Makefile":      "VPATH = src\nout.txt: input.txt\n\tprintf '%s' '$<' > out.txt\n",
		"src/input.txt": "input\n",
	}

	makeResult := runMakeLike(t, "make", files, "out.txt")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "out.txt")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

func TestCLIPrivateDirectiveAssignment(t *testing.T) {
	files := map[string]string{
		"Makefile": "MODE = debug\nprivate MODE := release\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n",
	}

	makeResult := runMakeLike(t, "make", files, "all")
	gomakeResult := runMakeLike(t, buildTestBinary(t), files, "-f", ".", "all")

	if makeResult.exitCode != gomakeResult.exitCode {
		t.Fatalf("exit code mismatch: make=%d gomake=%d\nmake:\n%s\ngomake:\n%s", makeResult.exitCode, gomakeResult.exitCode, makeResult.output, gomakeResult.output)
	}
	makeOut := readFileOrEmpty(t, filepath.Join(makeResult.dir, "out.txt"))
	gomakeOut := readFileOrEmpty(t, filepath.Join(gomakeResult.dir, "out.txt"))
	if makeOut != gomakeOut {
		t.Fatalf("artifact mismatch: make=%q gomake=%q", makeOut, gomakeOut)
	}
}

type runResult struct {
	dir      string
	output   string
	exitCode int
}

func runMakeLike(t *testing.T, program string, files map[string]string, args ...string) runResult {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	if filepath.Base(program) == "gomake" {
		cmd.Env = append(os.Environ(), testGoEnv(t)...)
	}
	output, err := cmd.CombinedOutput()
	result := runResult{
		dir:    dir,
		output: string(output),
	}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result
	}
	t.Fatalf("%s failed unexpectedly: %v\n%s", program, err, output)
	return runResult{}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

func normalizeRunOutput(output, dir string) string {
	return strings.ReplaceAll(output, dir, "$DIR")
}

func testGoEnv(t *testing.T) []string {
	t.Helper()
	goproxy := os.Getenv("GOMAKE_TEST_GOPROXY")
	if goproxy == "" {
		goproxy = "https://proxy.golang.org,direct"
	}
	gosumdb := os.Getenv("GOMAKE_TEST_GOSUMDB")
	root := repoRoot(t)
	return []string{
		fmt.Sprintf("GOCACHE=%s", filepath.Join(root, ".tmp", "gocache-test")),
		fmt.Sprintf("GOMODCACHE=%s", filepath.Join(root, ".tmp", "gomodcache-test")),
		fmt.Sprintf("GOPROXY=%s", goproxy),
		fmt.Sprintf("GOSUMDB=%s", gosumdb),
	}
}

func buildTestBinary(t *testing.T) string {
	t.Helper()

	root := repoRoot(t)
	cacheDir := filepath.Join(root, ".tmp", "gocache-test")
	modCacheDir := filepath.Join(root, ".tmp", "gomodcache-test")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(modCacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "gomake")
	cmd := exec.Command("go", "build", "-mod=mod", "-o", binPath, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), append(testGoEnv(t), "CGO_ENABLED=0")...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	return binPath
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	return root
}
