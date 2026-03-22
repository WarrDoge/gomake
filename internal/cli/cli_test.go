package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaybePrintDirectoryFromRecursiveMake(t *testing.T) {
	dir := t.TempDir()

	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
	})

	t.Setenv("MAKELEVEL", "1")
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("GNUMAKEFLAGS", "")

	done, err := maybePrintDirectory(dir)
	if err != nil {
		t.Fatalf("maybePrintDirectory() error = %v", err)
	}
	if done == nil {
		t.Fatalf("maybePrintDirectory() returned nil closer")
	}
	done()

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "make[1]: Entering directory '"+absDir+"'") {
		t.Fatalf("output = %q, want entering banner", got)
	}
	if !strings.Contains(got, "make[1]: Leaving directory '"+absDir+"'") {
		t.Fatalf("output = %q, want leaving banner", got)
	}
}

func TestShouldPrintDirectoryHonorsNoPrintDirectory(t *testing.T) {
	t.Setenv("MAKEFLAGS", "--no-print-directory")
	t.Setenv("GNUMAKEFLAGS", "")

	if shouldPrintDirectory(1) {
		t.Fatal("shouldPrintDirectory() = true, want false")
	}
}

func TestParseArgsSeparatesTargetsAndVariableOverrides(t *testing.T) {
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("GNUMAKEFLAGS", "")
	options, targets, overrides, shouldBuild, err := parseArgs([]string{"-f", ".", "all", "MODE=release", "test"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if options.RootDir != "." {
		t.Fatalf("RootDir = %q, want .", options.RootDir)
	}
	if got := strings.Join(targets, ","); got != "all,test" {
		t.Fatalf("targets = %q, want all,test", got)
	}
	if got := overrides["MODE"]; got != "release" {
		t.Fatalf("MODE = %q, want release", got)
	}
	if !strings.Contains(options.MakeFlags, "MODE=release") {
		t.Fatalf("MakeFlags = %q, want MODE=release", options.MakeFlags)
	}
}

func TestParseArgsSupportsDryRunKeepGoingTouchQuestionWhatIfEnvOverrideAndBuiltinDisables(t *testing.T) {
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("GNUMAKEFLAGS", "")
	options, targets, overrides, shouldBuild, err := parseArgs([]string{"-f", ".", "-e", "-r", "-R", "-p", "--warn-undefined-variables", "-n", "-k", "-t", "-q", "-W", "input.txt", "-W", "stamp", "all"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if !options.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if !options.KeepGoing {
		t.Fatal("KeepGoing = false, want true")
	}
	if !options.Touch {
		t.Fatal("Touch = false, want true")
	}
	if !options.Question {
		t.Fatal("Question = false, want true")
	}
	if !options.EnvOverride {
		t.Fatal("EnvOverride = false, want true")
	}
	if !options.PrintDatabase {
		t.Fatal("PrintDatabase = false, want true")
	}
	if !options.WarnUndefined {
		t.Fatal("WarnUndefined = false, want true")
	}
	if got := strings.Join(options.WhatIf, ","); got != "input.txt,stamp" {
		t.Fatalf("WhatIf = %q, want input.txt,stamp", got)
	}
	if !strings.Contains(options.MakeFlags, "--warn-undefined-variables") {
		t.Fatalf("MakeFlags = %q, want warn-undefined flag", options.MakeFlags)
	}
	if got := strings.Join(targets, ","); got != "all" {
		t.Fatalf("targets = %q, want all", got)
	}
	if len(overrides) != 0 {
		t.Fatalf("overrides = %v, want empty", overrides)
	}
}

func TestParseArgsSupportsParallelJobs(t *testing.T) {
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("GNUMAKEFLAGS", "")
	options, targets, overrides, shouldBuild, err := parseArgs([]string{"-f", ".", "-j", "4", "all"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if options.Jobs != 4 {
		t.Fatalf("Jobs = %d, want 4", options.Jobs)
	}
	if !strings.Contains(options.MakeFlags, "-j 4") {
		t.Fatalf("MakeFlags = %q, want -j propagation", options.MakeFlags)
	}
	if got := strings.Join(targets, ","); got != "all" {
		t.Fatalf("targets = %q, want all", got)
	}
	if len(overrides) != 0 {
		t.Fatalf("overrides = %v, want empty", overrides)
	}
}

func TestParseArgsReadsFlagsAndOverridesFromMakeflagsEnv(t *testing.T) {
	t.Setenv("MAKEFLAGS", "-kn -W stamp --warn-undefined-variables MODE=release")
	t.Setenv("GNUMAKEFLAGS", "")

	options, targets, overrides, shouldBuild, err := parseArgs([]string{"all"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if !options.KeepGoing {
		t.Fatal("KeepGoing = false, want true")
	}
	if !options.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if !options.WarnUndefined {
		t.Fatal("WarnUndefined = false, want true")
	}
	if got := strings.Join(options.WhatIf, ","); got != "stamp" {
		t.Fatalf("WhatIf = %q, want stamp", got)
	}
	if got := overrides["MODE"]; got != "release" {
		t.Fatalf("MODE = %q, want release", got)
	}
	if got := strings.Join(targets, ","); got != "all" {
		t.Fatalf("targets = %q, want all", got)
	}
}

func TestParseArgsReadsJobsFromMakeflagsEnv(t *testing.T) {
	t.Setenv("MAKEFLAGS", "-j3")
	t.Setenv("GNUMAKEFLAGS", "")

	options, _, _, shouldBuild, err := parseArgs([]string{"all"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if options.Jobs != 3 {
		t.Fatalf("Jobs = %d, want 3", options.Jobs)
	}
}

func TestParseArgsSupportsCommonLongOptionAliases(t *testing.T) {
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("GNUMAKEFLAGS", "")
	options, targets, overrides, shouldBuild, err := parseArgs([]string{
		"--file", ".",
		"--jobs", "3",
		"--silent",
		"--always-make",
		"--environment-overrides",
		"--no-builtin-rules",
		"--no-builtin-variables",
		"--print-data-base",
		"--dry-run",
		"--keep-going",
		"--touch",
		"--question",
		"--what-if", "stamp",
		"all",
	})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !shouldBuild {
		t.Fatal("shouldBuild = false, want true")
	}
	if options.Jobs != 3 {
		t.Fatalf("Jobs = %d, want 3", options.Jobs)
	}
	if options.Verbose {
		t.Fatal("Verbose = true, want false")
	}
	if !options.Rebuild || !options.EnvOverride || !options.NoBuiltinRules || !options.NoBuiltinVars || !options.PrintDatabase || !options.DryRun || !options.KeepGoing || !options.Touch || !options.Question {
		t.Fatalf("options = %#v, want long aliases to set all flags", options)
	}
	if got := strings.Join(options.WhatIf, ","); got != "stamp" {
		t.Fatalf("WhatIf = %q, want stamp", got)
	}
	if got := strings.Join(targets, ","); got != "all" {
		t.Fatalf("targets = %q, want all", got)
	}
	if len(overrides) != 0 {
		t.Fatalf("overrides = %v, want empty", overrides)
	}
}
