package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gomake/internal/build"
	"gomake/internal/config"
)

const version = "gomake 0.3.0"

type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Message
}

func (e *ExitError) ExitCode() int {
	if e.Code == 0 {
		return 1
	}
	return e.Code
}

func (e *ExitError) Silent() bool {
	return e.Message == ""
}

func Run(args []string) error {
	options, targets, overrides, shouldBuild, err := parseArgs(args)
	if err != nil {
		return err
	}
	if !shouldBuild {
		return nil
	}

	project, err := config.LoadWithContext(options.RootDir, config.LoadContext{
		Overrides: overrides,
		Goals:     targets,

		MakeProgram:         os.Args[0],
		MakeFlags:           options.MakeFlags,
		EnvironmentOverride: options.EnvOverride,
		WarnUndefined:       options.WarnUndefined,
		NoBuiltinRules:      options.NoBuiltinRules,
		NoBuiltinVars:       options.NoBuiltinVars,
	})
	if err != nil {
		return err
	}

	options.RootDir = filepath.Dir(project.SourcePath)
	done, err := maybePrintDirectory(options.RootDir)
	if err != nil {
		return err
	}
	if done != nil {
		defer done()
	}
	if options.PrintDatabase {
		printDatabase(project)
	}
	engine, err := build.New(project, options)
	if err != nil {
		return err
	}
	defer engine.SetupSignals()()

	if len(targets) == 0 {
		err := engine.Build("")
		engine.Cleanup()
		if options.Question && errors.Is(err, build.ErrOutOfDate) {
			return &ExitError{Code: 1}
		}
		return err
	}
	var allErrors []error
	for _, target := range targets {
		if err := engine.Build(target); err != nil {
			if options.Question && errors.Is(err, build.ErrOutOfDate) {
				engine.Cleanup()
				return &ExitError{Code: 1}
			}
			if !options.KeepGoing {
				engine.Cleanup()
				return err
			}
			allErrors = append(allErrors, err)
		}
	}
	engine.Cleanup()
	if len(allErrors) > 0 {
		return errors.Join(allErrors...)
	}
	return nil
}

func maybePrintDirectory(rootDir string) (func(), error) {
	level, _ := currentMakeLevel()
	if !shouldPrintDirectory(level) {
		return nil, nil
	}

	absDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	prefix := makePrefix(level)
	fmt.Fprintf(os.Stdout, "%s: Entering directory '%s'\n", prefix, absDir)
	return func() {
		fmt.Fprintf(os.Stdout, "%s: Leaving directory '%s'\n", prefix, absDir)
	}, nil
}

func currentMakeLevel() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("MAKELEVEL"))
	if raw == "" {
		return 0, false
	}
	level, err := strconv.Atoi(raw)
	if err != nil || level < 0 {
		return 0, false
	}
	return level, true
}

func shouldPrintDirectory(level int) bool {
	flags := os.Getenv("MAKEFLAGS") + " " + os.Getenv("GNUMAKEFLAGS")
	if strings.Contains(flags, "--no-print-directory") {
		return false
	}
	if level > 0 {
		return true
	}
	for _, field := range strings.Fields(flags) {
		if field == "--print-directory" {
			return true
		}
		if strings.HasPrefix(field, "-") && strings.Contains(field[1:], "w") {
			return true
		}
		if !strings.HasPrefix(field, "-") && strings.Contains(field, "w") {
			return true
		}
	}
	return false
}

func makePrefix(level int) string {
	if level <= 0 {
		return "make"
	}
	return fmt.Sprintf("make[%d]", level)
}

func parseArgs(args []string) (build.Options, []string, map[string]string, bool, error) {
	fs := flag.NewFlagSet("gomake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		filePath       string
		jobs           int
		silent         bool
		rebuild        bool
		envOverride    bool
		noBuiltinRules bool
		noBuiltinVars  bool
		printDatabase  bool
		warnUndefined  bool
		dryRun         bool
		keepGoing      bool
		ignoreErrors   bool
		touch          bool
		question       bool
		versionF       bool
		help           bool
		dirPaths       stringSliceFlag
		whatIf         stringSliceFlag
	)

	fs.StringVar(&filePath, "f", ".", "path to project file or directory")
	fs.StringVar(&filePath, "file", ".", "path to project file or directory")
	fs.StringVar(&filePath, "makefile", ".", "path to project file or directory")
	fs.Var(&dirPaths, "C", "change to directory before reading makefile")
	fs.Var(&dirPaths, "directory", "change to directory before reading makefile")
	fs.IntVar(&jobs, "j", -1, "number of jobs")
	fs.IntVar(&jobs, "jobs", -1, "number of jobs")
	fs.BoolVar(&silent, "s", false, "silent mode")
	fs.BoolVar(&silent, "silent", false, "silent mode")
	fs.BoolVar(&rebuild, "B", false, "unconditionally build targets")
	fs.BoolVar(&rebuild, "always-make", false, "unconditionally build targets")
	fs.BoolVar(&envOverride, "e", false, "environment overrides makefile variables")
	fs.BoolVar(&envOverride, "environment-overrides", false, "environment overrides makefile variables")
	fs.BoolVar(&noBuiltinRules, "r", false, "disable built-in rules")
	fs.BoolVar(&noBuiltinRules, "no-builtin-rules", false, "disable built-in rules")
	fs.BoolVar(&noBuiltinVars, "R", false, "disable built-in variables")
	fs.BoolVar(&noBuiltinVars, "no-builtin-variables", false, "disable built-in variables")
	fs.BoolVar(&printDatabase, "p", false, "print database")
	fs.BoolVar(&printDatabase, "print-data-base", false, "print database")
	fs.BoolVar(&warnUndefined, "warn-undefined-variables", false, "warn on undefined variables")
	fs.BoolVar(&dryRun, "n", false, "print recipes without executing")
	fs.BoolVar(&dryRun, "dry-run", false, "print recipes without executing")
	fs.BoolVar(&dryRun, "just-print", false, "print recipes without executing")
	fs.BoolVar(&dryRun, "recon", false, "print recipes without executing")
	fs.BoolVar(&keepGoing, "k", false, "keep going when some targets fail")
	fs.BoolVar(&keepGoing, "keep-going", false, "keep going when some targets fail")
	fs.BoolVar(&ignoreErrors, "i", false, "ignore errors from recipes")
	fs.BoolVar(&ignoreErrors, "ignore-errors", false, "ignore errors from recipes")
	fs.BoolVar(&touch, "t", false, "touch targets instead of running recipes")
	fs.BoolVar(&touch, "touch", false, "touch targets instead of running recipes")
	fs.BoolVar(&question, "q", false, "question mode; exit non-zero if out of date")
	fs.BoolVar(&question, "question", false, "question mode; exit non-zero if out of date")
	fs.Var(&whatIf, "W", "consider FILE to be recently modified")
	fs.Var(&whatIf, "what-if", "consider FILE to be recently modified")
	fs.Var(&whatIf, "new-file", "consider FILE to be recently modified")
	fs.Var(&whatIf, "assume-new", "consider FILE to be recently modified")
	fs.BoolVar(&versionF, "version", false, "print version")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")

	if err := fs.Parse(args); err != nil {
		return build.Options{}, nil, nil, false, err
	}
	if help {
		_, err := fmt.Fprintln(os.Stdout, "usage: gomake [options] [targets...]")
		return build.Options{}, nil, nil, false, err
	}
	if versionF {
		_, err := fmt.Fprintln(os.Stdout, version)
		return build.Options{}, nil, nil, false, err
	}
	envFlags := parseMakeFlagsEnv(os.Getenv("MAKEFLAGS"), os.Getenv("GNUMAKEFLAGS"))
	rebuild = rebuild || envFlags.Rebuild
	envOverride = envOverride || envFlags.EnvOverride
	noBuiltinRules = noBuiltinRules || envFlags.NoBuiltinRules
	noBuiltinVars = noBuiltinVars || envFlags.NoBuiltinVars
	printDatabase = printDatabase || envFlags.PrintDatabase
	warnUndefined = warnUndefined || envFlags.WarnUndefined
	dryRun = dryRun || envFlags.DryRun
	keepGoing = keepGoing || envFlags.KeepGoing
	ignoreErrors = ignoreErrors || envFlags.IgnoreErrors
	touch = touch || envFlags.Touch
	question = question || envFlags.Question
	silent = silent || envFlags.Silent
	whatIf = append(whatIf, envFlags.WhatIf...)
	if jobs <= 0 {
		if envFlags.Jobs > 0 {
			jobs = envFlags.Jobs
		} else {
			jobs = 1
		}
	}
	if jobs < 1 {
		return build.Options{}, nil, nil, false, errors.New("jobs must be at least 1")
	}

	for _, dp := range dirPaths {
		if dp == "" {
			continue
		}
		if err := os.Chdir(dp); err != nil {
			return build.Options{}, nil, nil, false, err
		}
	}

	root := filePath
	options := build.Options{
		RootDir:        root,
		Jobs:           jobs,
		Rebuild:        rebuild,
		Verbose:        !silent,
		EnvOverride:    envOverride,
		NoBuiltinRules: noBuiltinRules,
		NoBuiltinVars:  noBuiltinVars,
		PrintDatabase:  printDatabase,
		WarnUndefined:  warnUndefined,
		DryRun:         dryRun,
		KeepGoing:      keepGoing,
		IgnoreErrors:   ignoreErrors,
		Touch:          touch,
		Question:       question,
		WhatIf:         append([]string(nil), whatIf...),
	}
	targets, overrides := splitTargetsAndOverrides(fs.Args())
	for key, value := range envFlags.Overrides {
		if _, exists := overrides[key]; exists {
			continue
		}
		overrides[key] = value
	}
	options.MakeFlags = composeMakeFlags(options, overrides)
	return options, targets, overrides, true, nil
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, strings.TrimSpace(value))
	return nil
}

func splitTargetsAndOverrides(args []string) ([]string, map[string]string) {
	targets := make([]string, 0, len(args))
	overrides := map[string]string{}
	for _, arg := range args {
		if key, value, ok := parseVariableOverride(arg); ok {
			overrides[key] = value
			continue
		}
		targets = append(targets, arg)
	}
	return targets, overrides
}

func parseVariableOverride(arg string) (string, string, bool) {
	if strings.HasPrefix(arg, "=") {
		return "", "", false
	}
	idx := strings.Index(arg, "=")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(arg[:idx])
	if key == "" || strings.ContainsAny(key, " \t:") {
		return "", "", false
	}
	return key, arg[idx+1:], true
}

func composeMakeFlags(options build.Options, overrides map[string]string) string {
	flags := make([]string, 0, 16)
	if options.Jobs > 1 {
		flags = append(flags, "-j", strconv.Itoa(options.Jobs))
	}
	if !options.Verbose {
		flags = append(flags, "-s")
	}
	if options.Rebuild {
		flags = append(flags, "-B")
	}
	if options.EnvOverride {
		flags = append(flags, "-e")
	}
	if options.NoBuiltinRules {
		flags = append(flags, "-r")
	}
	if options.NoBuiltinVars {
		flags = append(flags, "-R")
	}
	if options.KeepGoing {
		flags = append(flags, "-k")
	}
	if options.IgnoreErrors {
		flags = append(flags, "-i")
	}
	if options.Touch {
		flags = append(flags, "-t")
	}
	if options.PrintDatabase {
		flags = append(flags, "-p")
	}
	if options.Question {
		flags = append(flags, "-q")
	}
	if options.WarnUndefined {
		flags = append(flags, "--warn-undefined-variables")
	}
	for _, value := range options.WhatIf {
		if strings.TrimSpace(value) == "" {
			continue
		}
		flags = append(flags, "-W", value)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		flags = append(flags, key+"="+overrides[key])
	}
	return strings.Join(flags, " ")
}

type makeFlagsFromEnv struct {
	Jobs           int
	Rebuild        bool
	EnvOverride    bool
	NoBuiltinRules bool
	NoBuiltinVars  bool
	PrintDatabase  bool
	WarnUndefined  bool
	DryRun         bool
	KeepGoing      bool
	IgnoreErrors   bool
	Touch          bool
	Question       bool
	Silent         bool
	WhatIf         []string
	Overrides      map[string]string
}

func parseMakeFlagsEnv(values ...string) makeFlagsFromEnv {
	out := makeFlagsFromEnv{Overrides: map[string]string{}}
	tokens := make([]string, 0)
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		tokens = append(tokens, strings.Fields(value)...)
	}

	for idx := 0; idx < len(tokens); idx++ {
		token := tokens[idx]
		if token == "-j" || token == "--jobs" {
			if idx+1 < len(tokens) {
				if value, ok := parsePositiveInt(tokens[idx+1]); ok {
					out.Jobs = value
					idx++
				}
			}
			continue
		}
		if strings.HasPrefix(token, "-j") && len(token) > 2 {
			if value, ok := parsePositiveInt(token[2:]); ok {
				out.Jobs = value
			}
			continue
		}
		if strings.HasPrefix(token, "--jobs=") {
			if value, ok := parsePositiveInt(strings.TrimSpace(strings.TrimPrefix(token, "--jobs="))); ok {
				out.Jobs = value
			}
			continue
		}
		if key, value, ok := parseVariableOverride(token); ok {
			out.Overrides[key] = value
			continue
		}
		switch token {
		case "--always-make":
			out.Rebuild = true
			continue
		case "--environment-overrides":
			out.EnvOverride = true
			continue
		case "--no-builtin-rules":
			out.NoBuiltinRules = true
			continue
		case "--no-builtin-variables":
			out.NoBuiltinVars = true
			continue
		case "--print-data-base":
			out.PrintDatabase = true
			continue
		case "--dry-run", "--just-print", "--recon":
			out.DryRun = true
			continue
		case "--keep-going":
			out.KeepGoing = true
			continue
		case "--ignore-errors":
			out.IgnoreErrors = true
			continue
		case "--question":
			out.Question = true
			continue
		case "--silent":
			out.Silent = true
			continue
		case "--touch":
			out.Touch = true
			continue
		case "--warn-undefined-variables":
			out.WarnUndefined = true
			continue
		case "--what-if", "--new-file", "--assume-new":
			if idx+1 < len(tokens) {
				idx++
				out.WhatIf = append(out.WhatIf, strings.TrimSpace(tokens[idx]))
			}
			continue
		case "-W":
			if idx+1 < len(tokens) {
				idx++
				out.WhatIf = append(out.WhatIf, strings.TrimSpace(tokens[idx]))
			}
			continue
		}
		if strings.HasPrefix(token, "-W") && len(token) > 2 {
			out.WhatIf = append(out.WhatIf, strings.TrimSpace(token[2:]))
			continue
		}
		if strings.HasPrefix(token, "--what-if=") {
			out.WhatIf = append(out.WhatIf, strings.TrimSpace(strings.TrimPrefix(token, "--what-if=")))
			continue
		}
		if strings.HasPrefix(token, "--new-file=") {
			out.WhatIf = append(out.WhatIf, strings.TrimSpace(strings.TrimPrefix(token, "--new-file=")))
			continue
		}
		if strings.HasPrefix(token, "--assume-new=") {
			out.WhatIf = append(out.WhatIf, strings.TrimSpace(strings.TrimPrefix(token, "--assume-new=")))
			continue
		}
		if strings.HasPrefix(token, "-") {
			for _, ch := range token[1:] {
				switch ch {
				case 'B':
					out.Rebuild = true
				case 'e':
					out.EnvOverride = true
				case 'k':
					out.KeepGoing = true
				case 'i':
					out.IgnoreErrors = true
				case 'n':
					out.DryRun = true
				case 'p':
					out.PrintDatabase = true
				case 'q':
					out.Question = true
				case 's':
					out.Silent = true
				case 't':
					out.Touch = true
				case 'r':
					out.NoBuiltinRules = true
				case 'R':
					out.NoBuiltinVars = true
				}
			}
		}
	}
	return out
}

func parsePositiveInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func printDatabase(project *config.Project) {
	fmt.Fprintln(os.Stdout, "# Variables")
	varNames := make([]string, 0, len(project.Variables))
	for name := range project.Variables {
		varNames = append(varNames, name)
	}
	sort.Strings(varNames)
	for _, name := range varNames {
		fmt.Fprintf(os.Stdout, "%s = %s\n", name, project.Variables[name])
	}

	fmt.Fprintln(os.Stdout, "# Targets")
	for _, target := range project.Targets {
		deps := strings.Join(target.Deps, " ")
		if len(target.OrderOnlyDeps) > 0 {
			if deps != "" {
				deps += " "
			}
			deps += "| " + strings.Join(target.OrderOnlyDeps, " ")
		}
		fmt.Fprintf(os.Stdout, "%s: %s\n", target.Name, strings.TrimSpace(deps))
	}
}
