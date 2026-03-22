package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gomake/internal/config"
)

type Options struct {
	RootDir        string
	Jobs           int
	Rebuild        bool
	Verbose        bool
	EnvOverride    bool
	NoBuiltinRules bool
	NoBuiltinVars  bool
	PrintDatabase  bool
	WarnUndefined  bool
	MakeFlags      string
	DryRun         bool
	KeepGoing      bool
	Touch          bool
	Question       bool
	IgnoreErrors   bool
	WhatIf         []string
}

var ErrOutOfDate = errors.New("target is out of date")

type Engine struct {
	project            *config.Project
	options            Options
	targets            map[string][]config.Target
	patternRules       []config.Target
	defaultRule        *config.Target
	whatIf             map[string]bool
	built              map[string]bool
	expandedCache      map[string][]string
	effectiveVars      map[string]map[string]string
	effectiveFlavors   map[string]map[string]config.VariableFlavor
	effectiveOverrides map[string]map[string]bool
	runningRules       sync.Map // map[string]config.Target
	interrupted        bool
	interruptOnce      sync.Once
}

func New(project *config.Project, options Options) (*Engine, error) {
	targets := make(map[string][]config.Target)
	for _, target := range project.Targets {
		targets[target.Name] = append(targets[target.Name], target)
	}
	patternRules := make([]config.Target, len(project.PatternRules))
	copy(patternRules, project.PatternRules)

	var defaultRule *config.Target
	if rules, ok := targets[".DEFAULT"]; ok && len(rules) > 0 {
		ruleCopy := rules[0]
		defaultRule = &ruleCopy
	}
	whatIf := make(map[string]bool, len(options.WhatIf))
	for _, file := range options.WhatIf {
		if file == "" {
			continue
		}
		whatIf[file] = true
	}
	if options.RootDir == "" {
		options.RootDir = "."
	}
	if project.NotParallel && options.Jobs > 1 {
		options.Jobs = 1
	}
	return &Engine{
		project:            project,
		options:            options,
		targets:            targets,
		patternRules:       patternRules,
		defaultRule:        defaultRule,
		whatIf:             whatIf,
		built:              make(map[string]bool),
		expandedCache:      make(map[string][]string),
		effectiveVars:      make(map[string]map[string]string),
		effectiveFlavors:   make(map[string]map[string]config.VariableFlavor),
		effectiveOverrides: make(map[string]map[string]bool),
	}, nil
}

func (e *Engine) SetupSignals() func() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigCh:
			fmt.Fprintf(os.Stderr, "\ngomake: *** Deleting intermediate files... interrupted by signal %v\n", sig)
			e.interruptOnce.Do(func() {
				e.interrupted = true
				e.runningRules.Range(func(key, value any) bool {
					target := value.(config.Target)
					e.cleanupTargetOnError(target, true)
					return true
				})
			})
			os.Exit(130) // standard exit code for SIGINT
		case <-ctx.Done():
		}
	}()

	return func() {
		cancel()
		signal.Stop(sigCh)
	}
}

func (e *Engine) findMatchingRule(targetName string) (config.Target, bool) {
	return e.findMatchingRuleRecursive(targetName, make(map[string]bool))
}

func (e *Engine) findMatchingRuleRecursive(targetName string, visited map[string]bool) (config.Target, bool) {
	if visited[targetName] {
		return config.Target{}, false
	}
	visited[targetName] = true
	defer func() { visited[targetName] = false }()

	for _, rule := range e.patternRules {
		stem, ok := matchPattern(rule.Name, targetName)
		if !ok {
			continue
		}
		concrete := rule
		concrete.Name = targetName
		concrete.Deps = expandPatternPrerequisites(rule.Deps, stem)
		concrete.OrderOnlyDeps = expandPatternPrerequisites(rule.OrderOnlyDeps, stem)

		if e.canSatisfyPrerequisitesRecursive(concrete, visited) {
			return concrete, true
		}
	}
	return config.Target{}, false
}

func (e *Engine) canSatisfyPrerequisitesRecursive(target config.Target, visited map[string]bool) bool {
	for _, dep := range e.getAllPrerequisites(target) {
		if _, ok := e.targets[dep]; ok {
			continue
		}
		if _, _, err := e.statPrerequisite(dep); err == nil {
			continue
		}
		if _, ok := e.findMatchingRuleRecursive(dep, visited); ok {
			continue
		}
		return false
	}
	return true
}

func (e *Engine) ensureConcreteTarget(name string) (config.Target, bool) {
	if rules, ok := e.targets[name]; ok {
		if len(rules) == 0 {
			return config.Target{}, false
		}
		if len(rules) == 1 && !rules[0].DoubleColon && len(rules[0].Commands) == 0 {
			if rule, ok := e.findMatchingRule(name); ok {
				rules[0].Commands = rule.Commands
				rules[0].Deps = appendUnique(rules[0].Deps, rule.Deps...)
				rules[0].OrderOnlyDeps = appendUnique(rules[0].OrderOnlyDeps, rule.OrderOnlyDeps...)
				e.targets[name] = rules
				return rules[0], true
			}
		}
		return rules[0], true
	}
	if rule, ok := e.findMatchingRule(name); ok {
		rule.Intermediate = true
		e.targets[name] = []config.Target{rule}
		return rule, true
	}
	return config.Target{}, false
}

func (e *Engine) canSatisfyPrerequisites(target config.Target) bool {
	return e.canSatisfyPrerequisitesRecursive(target, make(map[string]bool))
}

func (e *Engine) Build(targetName string) error {
	if targetName == "" {
		targetName = e.project.DefaultTarget
	}
	if _, ok := e.ensureConcreteTarget(targetName); !ok {
		return e.buildUnknownTarget(targetName)
	}

	order, err := e.resolve(targetName)
	if err != nil {
		return err
	}
	if e.options.Jobs <= 1 || len(order) <= 1 {
		return e.buildSequential(order)
	}
	return e.buildParallel(order)
}

func (e *Engine) buildSequential(order []string) error {
	failed := map[string]bool{}
	var allErrors []error
	for _, name := range order {
		err := e.buildResolvedTarget(name, failed)
		if err != nil {
			failed[name] = true
			if !e.options.KeepGoing {
				return err
			}
			allErrors = append(allErrors, err)
			continue
		}
	}
	if len(allErrors) > 0 {
		return errors.Join(allErrors...)
	}
	return nil
}

type targetExecutionResult struct {
	name string
	err  error
}

func (e *Engine) buildParallel(order []string) error {
	jobs := e.options.Jobs
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(order) {
		jobs = len(order)
	}

	known := make(map[string]bool, len(order))
	for _, name := range order {
		known[name] = true
	}

	pending := make(map[string]int, len(order))
	dependents := make(map[string][]string, len(order))
	for _, name := range order {
		for _, target := range e.targets[name] {
			for _, dep := range e.getAllPrerequisites(target) {
				if !known[dep] {
					continue
				}
				pending[name]++
				dependents[dep] = append(dependents[dep], name)
			}
		}
	}

	resultCh := make(chan targetExecutionResult, len(order))
	completed := make(map[string]bool, len(order))
	failed := make(map[string]bool)
	enqueued := map[string]bool{}
	readyQueue := make([]string, 0, len(order))
	processed := 0
	running := 0
	stopped := false
	var firstErr error
	var allErrors []error

	var complete func(name string, err error)
	complete = func(name string, err error) {
		if completed[name] {
			return
		}
		completed[name] = true
		processed++
		if err != nil {
			failed[name] = true
			if !e.options.KeepGoing {
				if firstErr == nil {
					firstErr = err
				}
				stopped = true
			} else {
				allErrors = append(allErrors, err)
			}
		}

		for _, dep := range dependents[name] {
			pending[dep]--
			if pending[dep] != 0 || completed[dep] || enqueued[dep] {
				continue
			}

			if stopped {
				continue
			}
			readyQueue = append(readyQueue, dep)
		}
	}

	scheduleReady := func() {
		for running < jobs && len(readyQueue) > 0 && !stopped {
			name := readyQueue[0]
			readyQueue = readyQueue[1:]

			// Check for failed prerequisites in the main goroutine
			// where we have exclusive access to the failed map.
			skipTarget := false
			for _, target := range e.targets[name] {
				if e.hasFailedPrerequisite(target, failed) {
					complete(name, fmt.Errorf("target %q not remade because of prerequisite errors", name))
					skipTarget = true
					break
				}
			}
			if skipTarget {
				continue
			}

			enqueued[name] = true
			running++
			go func(targetName string) {
				resultCh <- targetExecutionResult{name: targetName, err: e.buildResolvedTarget(targetName, nil)}
			}(name)
		}
	}

	for _, name := range order {
		if pending[name] != 0 {
			continue
		}
		readyQueue = append(readyQueue, name)
	}
	scheduleReady()

	for running > 0 {
		result := <-resultCh
		running--
		enqueued[result.name] = false
		complete(result.name, result.err)
		scheduleReady()
	}

	if !e.options.KeepGoing {
		return firstErr
	}
	if len(allErrors) > 0 {
		return errors.Join(allErrors...)
	}
	if processed < len(order) {
		return errors.New("parallel build terminated before processing all targets")
	}
	return nil
}

func (e *Engine) buildResolvedTarget(name string, failed map[string]bool) error {
	for _, target := range e.targets[name] {
		if failed != nil && e.hasFailedPrerequisite(target, failed) {
			return fmt.Errorf("target %q not remade because of prerequisite errors", name)
		}
		if err := e.buildResolvedRule(target); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) buildResolvedRule(target config.Target) error {
	if e.built[target.Name] {
		return nil
	}
	if err := e.validatePrerequisites(target); err != nil {
		return err
	}
	if !e.shouldRunRule(target) {
		return nil
	}
	if e.options.Question {
		return fmt.Errorf("%w: %s", ErrOutOfDate, target.Name)
	}
	if err := e.runTarget(target); err != nil {
		return err
	}
	return nil
}

func (e *Engine) buildUnknownTarget(targetName string) error {
	if _, err := e.statTarget(targetName); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) && !errors.Is(err, errArchiveMemberNotFound) {
		return fmt.Errorf("stat target %s: %w", targetName, err)
	}
	if !e.hasDefaultRule() {
		return fmt.Errorf("unknown target %q", targetName)
	}
	if e.options.Question {
		return fmt.Errorf("%w: %s", ErrOutOfDate, targetName)
	}
	if err := e.runDefaultRule(targetName); err != nil {
		return fmt.Errorf("build target %q via .DEFAULT: %w", targetName, err)
	}
	return nil
}

func (e *Engine) hasFailedPrerequisite(target config.Target, failed map[string]bool) bool {
	for _, dep := range e.getAllPrerequisites(target) {
		if failed[dep] {
			return true
		}
	}
	return false
}

func (e *Engine) resolve(targetName string) ([]string, error) {
	var order []string
	perm := map[string]bool{}
	temp := map[string]bool{}

	var visit func(name string, parentVars map[string]string, parentFlavors map[string]config.VariableFlavor, parentOverrides map[string]bool) error
	visit = func(name string, parentVars map[string]string, parentFlavors map[string]config.VariableFlavor, parentOverrides map[string]bool) error {
		var effVars map[string]string
		var effFlavors map[string]config.VariableFlavor
		var effOverrides map[string]bool

		// Calculate effective variables for this target name
		// We use the first parent that encounters it
		if _, ok := e.effectiveVars[name]; !ok {
			vars := make(map[string]string)
			flavors := make(map[string]config.VariableFlavor)
			overrides := make(map[string]bool)
			for k, v := range parentVars {
				vars[k] = v
				flavors[k] = parentFlavors[k]
				overrides[k] = parentOverrides[k]
			}
			// Merge pattern-specific variables
			for _, pattern := range e.project.PatternOrder {
				if _, ok := matchPattern(pattern, name); ok {
					pVars := e.project.PatternVars[pattern]
					pFlavors := e.project.PatternFlavors[pattern]
					pOverrides := e.project.PatternOverrides[pattern]
					for k, v := range pVars {
						vars[k] = v
						flavors[k] = pFlavors[k]
						overrides[k] = pOverrides[k]
					}
				}
			}
			// Merge target-specific variables from all rules for this name
			for _, target := range e.targets[name] {
				for k, v := range target.Variables {
					vars[k] = v
					flavors[k] = target.VariableFlavors[k]
					overrides[k] = target.OverrideSet[k]
				}
			}
			e.effectiveVars[name] = vars
			e.effectiveFlavors[name] = flavors
			e.effectiveOverrides[name] = overrides
		}

		// Re-calculate child versions (excluding private variables)
		vVars := e.effectiveVars[name]
		vFlavors := e.effectiveFlavors[name]
		vOverrides := e.effectiveOverrides[name]

		childVars := make(map[string]string)
		childFlavors := make(map[string]config.VariableFlavor)
		childOverrides := make(map[string]bool)
		for k, v := range vVars {
			if e.project.PrivateVariables[k] {
				continue
			}
			isPrivate := false
			for _, target := range e.targets[name] {
				if target.PrivateVariables[k] {
					isPrivate = true
					break
				}
			}
			if isPrivate {
				continue
			}
			for _, pattern := range e.project.PatternOrder {
				if _, ok := matchPattern(pattern, name); ok {
					if privs, ok := e.project.PatternPrivateVars[pattern]; ok && privs[k] {
						isPrivate = true
						break
					}
				}
			}
			if isPrivate {
				continue
			}
			childVars[k] = v
			childFlavors[k] = vFlavors[k]
			childOverrides[k] = vOverrides[k]
		}
		effVars = childVars
		effFlavors = childFlavors
		effOverrides = childOverrides

		if perm[name] {
			return nil
		}
		if temp[name] {
			return fmt.Errorf("dependency cycle detected at %q", name)
		}
		temp[name] = true

		for _, target := range e.targets[name] {
			for _, dep := range e.getAllPrerequisites(target) {
				if _, ok := e.ensureConcreteTarget(dep); !ok {
					continue
				}
				if err := visit(dep, effVars, effFlavors, effOverrides); err != nil {
					return err
				}
			}
		}
		temp[name] = false
		perm[name] = true
		order = append(order, name)
		return nil
	}

	if err := visit(targetName, e.project.Variables, e.project.VariableFlavors, nil); err != nil {
		return nil, err
	}
	return order, nil
}

func (e *Engine) validatePrerequisites(target config.Target) error {
	for _, dep := range e.getAllPrerequisites(target) {
		if _, ok := e.targets[dep]; ok {
			continue
		}
		_, _, err := e.statPrerequisite(dep)
		if err == nil {
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			if e.hasDefaultRule() {
				if buildErr := e.runDefaultRule(dep); buildErr != nil {
					return fmt.Errorf("build prerequisite %q via .DEFAULT: %w", dep, buildErr)
				}
				if _, _, statErr := e.statPrerequisite(dep); statErr == nil {
					continue
				} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) && !os.IsNotExist(statErr) {
					return fmt.Errorf("stat prerequisite %s: %w", dep, statErr)
				}
			}
			return fmt.Errorf("missing prerequisite %q for target %q", dep, target.Name)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat prerequisite %s: %w", dep, err)
		}
		return fmt.Errorf("missing prerequisite %q for target %q", dep, target.Name)
	}
	return nil
}

func (e *Engine) shouldRunRule(target config.Target) bool {
	if e.options.Rebuild || target.Phony {
		return true
	}
	if e.whatIf[target.Name] {
		return true
	}

	targetInfo, err := e.statTarget(target.Name)
	if err != nil {
		return true
	}

	for _, dep := range e.getDeps(target) {
		if e.whatIf[dep] {
			return true
		}
		if depRules, ok := e.targets[dep]; ok {
			isPhony := false
			for _, r := range depRules {
				if r.Phony {
					isPhony = true
					break
				}
			}
			if isPhony {
				return true
			}
		}

		resolved, depInfo, err := e.statPrerequisite(dep)
		if err != nil {
			return true
		}
		if e.whatIf[resolved] {
			return true
		}

		targetTime := targetInfo.ModTime()
		depTime := depInfo.ModTime()
		// If either target is an archive member or we know we're dealing with low resolution time,
		// we should probably truncate to seconds.
		if target.LowResolutionTime || depInfo.Mode()&os.ModeType != 0 {
			// If it's a special file or marked as low res, truncate
			targetTime = targetTime.Truncate(time.Second)
			depTime = depTime.Truncate(time.Second)
		} else if _, _, ok := parseArchiveTarget(target.Name); ok {
			targetTime = targetTime.Truncate(time.Second)
			depTime = depTime.Truncate(time.Second)
		}

		if depTime.After(targetTime) {
			return true
		}
	}

	return false
}

func (e *Engine) runTarget(target config.Target) error {
	e.built[target.Name] = true
	for _, groupTarget := range target.GroupTargets {
		e.built[groupTarget] = true
	}
	e.runningRules.Store(target.Name, target)
	defer e.runningRules.Delete(target.Name)

	if e.options.Touch {
		return e.touchTarget(target)
	}

	effVars := e.effectiveVars[target.Name]
	effFlavors := e.effectiveFlavors[target.Name]

	if len(target.Commands) == 0 {
		if target.Phony {
			return nil
		}
		if _, err := e.statTarget(target.Name); err == nil {
			return nil
		}
		if e.hasDefaultRule() {
			return e.runDefaultRule(target.Name)
		}
		return fmt.Errorf("target %q is out of date but has no commands", target.Name)
	}

	env := e.commandEnvironment(target.Name)
	if e.project.OneShell {
		var runnable []string
		globalSilent := false
		globalIgnore := false
		globalForce := false

		for i, command := range target.Commands {
			expanded := e.expandAutomaticVars(target, command.Text, effVars, effFlavors)
			if expanded == "" {
				continue
			}

			if i == 0 {
				globalSilent = command.Silent
				globalIgnore = command.IgnoreError
				globalForce = command.Force
			}

			if e.shouldPrintCommandExplicit(target, globalSilent, command.Silent) {
				fmt.Fprintln(os.Stdout, expanded)
			}
			if e.options.DryRun && !globalForce && !command.Force && !command.Recursive {
				continue
			}
			runnable = append(runnable, expanded)
		}
		if len(runnable) == 0 {
			return nil
		}
		if err := runShellCommand(e.options.RootDir, e.project.Shell, e.project.ShellFlags, env, strings.Join(runnable, "\n")); err != nil && !target.IgnoreErrors && !globalIgnore && !e.options.IgnoreErrors {
			e.cleanupTargetOnError(target, false)
			return fmt.Errorf("target %s: %w", target.Name, err)
		}
		return nil
	}

	for _, command := range target.Commands {
		expanded := e.expandAutomaticVars(target, command.Text, effVars, effFlavors)
		if expanded == "" {
			continue
		}
		if e.shouldPrintCommand(target, command) {
			fmt.Fprintln(os.Stdout, expanded)
		}

		if e.options.DryRun && !command.Force && !command.Recursive {
			continue
		}

		if err := runShellCommand(e.options.RootDir, e.project.Shell, e.project.ShellFlags, env, expanded); err != nil {
			if (command.IgnoreError || target.IgnoreErrors || e.options.IgnoreErrors) && isShellExitStatus(err) {
				continue
			}
			e.cleanupTargetOnError(target, false)
			return fmt.Errorf("target %s: %w", target.Name, err)
		}
	}
	return nil
}

func (e *Engine) Cleanup() {
	if e.options.DryRun {
		return
	}
	var intermediates []string
	for name, rules := range e.targets {
		isIntermediate := true
		isSecondary := false
		for _, rule := range rules {
			if !rule.Intermediate {
				isIntermediate = false
			}
			if rule.Secondary {
				isSecondary = true
			}
		}
		if isIntermediate && !isSecondary && e.built[name] {
			intermediates = append(intermediates, name)
		}
	}
	if len(intermediates) == 0 {
		return
	}
	sort.Strings(intermediates)
	fmt.Fprintf(os.Stdout, "rm %s\n", strings.Join(intermediates, " "))
	for _, name := range intermediates {
		if err := os.Remove(e.absPath(name)); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: failed to remove intermediate file %s: %v\n", name, err)
		}
	}
}

func (e *Engine) cleanupTargetOnError(target config.Target, isSignal bool) {
	if target.Phony || target.Precious {
		return
	}
	if !isSignal && !target.DeleteOnError {
		return
	}
	if err := os.Remove(e.absPath(target.Name)); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: failed to remove target %s after error: %v\n", target.Name, err)
	}
}

func (e *Engine) shouldPrintCommand(target config.Target, command config.RecipeCommand) bool {
	return e.shouldPrintCommandExplicit(target, false, command.Silent)
}

func (e *Engine) shouldPrintCommandExplicit(target config.Target, globalSilent, commandSilent bool) bool {
	if e.options.DryRun {
		return true
	}
	if target.Silent || globalSilent {
		return false
	}
	return e.options.Verbose && !commandSilent
}

func (e *Engine) expandAutomaticVars(target config.Target, command string, effVars map[string]string, effFlavors map[string]config.VariableFlavor) string {
	const escapedDollar = "\x00"
	command = strings.ReplaceAll(command, "$$", escapedDollar)
	auto := e.automaticContext(target)

	// Combine effective variables with automatic variables
	vars := make(map[string]string, len(effVars)+len(auto))
	flavors := make(map[string]config.VariableFlavor, len(effFlavors)+len(auto))
	for k, v := range effVars {
		vars[k] = v
		flavors[k] = effFlavors[k]
	}
	for k, v := range auto {
		vars[k] = v
		flavors[k] = config.FlavorSimple
	}

	command = config.ExpandVarsWithOptions(command, vars, flavors, nil, true, false, nil, nil)
	return strings.ReplaceAll(command, escapedDollar, "$")
}

type automaticContext map[string]string

func (e *Engine) automaticContext(target config.Target) automaticContext {
	deps := e.getDeps(target)
	oodeps := e.getOrderOnlyDeps(target)
	resolvedDeps := make([]string, 0, len(deps))
	for _, dep := range deps {
		resolvedDeps = append(resolvedDeps, e.prerequisiteReference(dep))
	}
	resolvedOrderOnly := make([]string, 0, len(oodeps))
	for _, dep := range oodeps {
		resolvedOrderOnly = append(resolvedOrderOnly, e.prerequisiteReference(dep))
	}

	firstPrerequisite := ""
	if len(resolvedDeps) > 0 {
		firstPrerequisite = resolvedDeps[0]
	}

	targetName := target.Name
	memberName := ""
	if archive, member, ok := parseArchiveTarget(target.Name); ok {
		targetName = archive
		memberName = member
	}

	values := automaticContext{
		"@": targetName,
		"%": memberName,
		"<": firstPrerequisite,
		"^": strings.Join(uniqueStrings(resolvedDeps), " "),
		"+": strings.Join(resolvedDeps, " "),
		"?": strings.Join(e.newerPrerequisites(target), " "),
		"|": strings.Join(uniqueStrings(resolvedOrderOnly), " "),
		"*": targetStem(targetName),
	}

	for _, key := range []string{"@", "%", "<", "^", "+", "?", "*"} {
		values[key+"D"] = wordDirs(values[key])
		values[key+"F"] = wordFiles(values[key])
	}

	return values
}

func (e *Engine) newerPrerequisites(target config.Target) []string {
	if len(target.Deps) == 0 {
		return nil
	}

	targetInfo, err := e.statTarget(target.Name)
	targetExists := err == nil
	deps := e.getDeps(target)
	newer := make([]string, 0, len(deps))
	for _, dep := range deps {
		if depRules, ok := e.targets[dep]; ok {
			isPhony := false
			for _, r := range depRules {
				if r.Phony {
					isPhony = true
					break
				}
			}
			if isPhony {
				newer = append(newer, dep)
				continue
			}
		}

		resolved, depInfo, err := e.statPrerequisite(dep)
		if err != nil || !targetExists || depInfo.ModTime().After(targetInfo.ModTime()) {
			newer = append(newer, resolved)
		}
	}

	return newer
}

func targetStem(target string) string {
	ext := filepath.Ext(target)
	if ext == "" {
		return ""
	}
	return strings.TrimSuffix(target, ext)
}

func wordDirs(value string) string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	for i, word := range words {
		dir := filepath.Dir(word)
		if dir == "" {
			dir = "."
		}
		words[i] = dir
	}
	return strings.Join(words, " ")
}

func wordFiles(value string) string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	for i, word := range words {
		words[i] = filepath.Base(word)
	}
	return strings.Join(words, " ")
}

func (e *Engine) touchTarget(target config.Target) error {
	if target.Phony {
		return nil
	}

	targetsToTouch := append([]string{target.Name}, target.GroupTargets...)
	for _, name := range uniqueStrings(targetsToTouch) {
		path := e.absPath(name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create target directory for %q: %w", name, err)
		}
		now := time.Now()
		if _, err := os.Stat(path); err == nil {
			if err := os.Chtimes(path, now, now); err != nil {
				return fmt.Errorf("touch %q: %w", name, err)
			}
			continue
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return fmt.Errorf("touch %q: %w", name, err)
		}
	}
	return nil
}

func (e *Engine) commandEnvironment(targetName string) []string {
	base := os.Environ()
	overrides := map[string]string{}
	// Global variables
	for name, value := range e.project.Variables {
		if e.project.UnexportedVariables[name] {
			continue
		}
		if e.project.ExportAllVariables || e.project.ExportedVariables[name] {
			overrides[name] = value
		}
	}
	// Target-specific inherited variables
	if effVars, ok := e.effectiveVars[targetName]; ok {
		effFlavors := e.effectiveFlavors[targetName]
		for name, val := range effVars {
			// GNU Make says: "Target-specific variables are exported just like any other variables"
			// But they only override if not overridden by command line (unless 'override')

			// For now, let's assume they are exported if global flag is set or explicitly exported
			if e.project.UnexportedVariables[name] {
				continue
			}
			if e.project.ExportAllVariables || e.project.ExportedVariables[name] {
				// Resolve the value in the context of effective variables
				resolved := config.ExpandVarsWithOptions(val, effVars, effFlavors, nil, true, true, nil, nil)
				overrides[name] = resolved
			}
		}
	}
	for _, name := range []string{"MAKE", "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKECMDGOALS"} {
		if value, ok := e.project.Variables[name]; ok {
			overrides[name] = value
		}
	}
	if len(overrides) == 0 && len(e.project.UnexportedVariables) == 0 {
		return base
	}

	merged := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	removed := map[string]bool{}
	for name := range e.project.UnexportedVariables {
		removed[name] = true
	}
	for _, entry := range base {
		idx := strings.Index(entry, "=")
		if idx <= 0 {
			merged = append(merged, entry)
			continue
		}
		key := entry[:idx]
		if removed[key] {
			continue
		}
		if value, ok := overrides[key]; ok {
			merged = append(merged, key+"="+value)
			seen[key] = true
			continue
		}
		merged = append(merged, entry)
	}
	for key, value := range overrides {
		if seen[key] {
			continue
		}
		merged = append(merged, key+"="+value)
	}
	return merged
}

func (e *Engine) prerequisiteReference(name string) string {
	resolved, _, err := e.statPrerequisite(name)
	if err != nil {
		return name
	}
	return resolved
}

func (e *Engine) statPrerequisite(name string) (string, os.FileInfo, error) {
	resolved := name
	info, err := e.statTarget(resolved)
	if err == nil {
		return resolved, info, nil
	}
	if !os.IsNotExist(err) && !errors.Is(err, errArchiveMemberNotFound) {
		return resolved, nil, err
	}

	for _, entry := range e.project.VPaths {
		if !matchVPathPattern(entry.Pattern, name) {
			continue
		}
		for _, dir := range entry.Directories {
			candidate := filepath.Join(dir, name)
			info, err = e.statTarget(candidate)
			if err == nil {
				return candidate, info, nil
			}
			if !os.IsNotExist(err) && !errors.Is(err, errArchiveMemberNotFound) {
				return candidate, nil, err
			}
		}
	}
	for _, dir := range splitSearchDirectories(e.project.Variables["VPATH"]) {
		candidate := filepath.Join(dir, name)
		info, err = e.statTarget(candidate)
		if err == nil {
			return candidate, info, nil
		}
		if !os.IsNotExist(err) && !errors.Is(err, errArchiveMemberNotFound) {
			return candidate, nil, err
		}
	}

	return name, nil, os.ErrNotExist
}

func (e *Engine) statTarget(name string) (os.FileInfo, error) {
	if archive, member, ok := parseArchiveTarget(name); ok {
		modTime, err := statArchiveMember(e.absPath(archive), member)
		if err != nil {
			return nil, err
		}
		// Return a fake FileInfo just for the mod time
		return fakeFileInfo{name: name, modTime: modTime}, nil
	}
	return os.Stat(e.absPath(name))
}

type fakeFileInfo struct {
	name    string
	modTime time.Time
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0644 }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func (e *Engine) hasDefaultRule() bool {
	return e.defaultRule != nil && len(e.defaultRule.Commands) > 0
}

func (e *Engine) runDefaultRule(name string) error {
	if !e.hasDefaultRule() {
		return fmt.Errorf("unknown target %q", name)
	}
	target := e.defaultRuleFor(name)
	return e.runTarget(target)
}

func (e *Engine) defaultRuleFor(name string) config.Target {
	return config.Target{
		Name:          name,
		Deps:          append([]string(nil), e.defaultRule.Deps...),
		OrderOnlyDeps: append([]string(nil), e.defaultRule.OrderOnlyDeps...),
		Commands:      append([]config.RecipeCommand(nil), e.defaultRule.Commands...),
		Phony:         false,
		Silent:        e.defaultRule.Silent,
		IgnoreErrors:  e.defaultRule.IgnoreErrors,
	}
}

func matchVPathPattern(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		return pattern == name
	}
	prefix := pattern[:idx]
	suffix := pattern[idx+1:]
	if len(name) < len(prefix)+len(suffix) {
		return false
	}
	return strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix)
}

func splitSearchDirectories(raw string) []string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return nil
	}
	sep := string(os.PathListSeparator)
	directories := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, dir := range strings.Split(part, sep) {
			dir = strings.TrimSpace(dir)
			if dir == "" {
				continue
			}
			directories = append(directories, dir)
		}
	}
	return directories
}

func matchPattern(pattern, name string) (string, bool) {
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		return "", false
	}
	prefix := pattern[:idx]
	suffix := pattern[idx+1:]
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	if len(name) < len(prefix)+len(suffix) {
		return "", false
	}
	return name[len(prefix) : len(name)-len(suffix)], true
}

func expandPatternPrerequisites(deps []string, stem string) []string {
	out := make([]string, len(deps))
	for i, dep := range deps {
		out[i] = strings.ReplaceAll(dep, "%", stem)
	}
	return out
}

func (e *Engine) absPath(path string) string {
	return filepath.Join(e.options.RootDir, path)
}

func appendUnique(target []string, items ...string) []string {
	for _, item := range items {
		found := false
		for _, v := range target {
			if v == item {
				found = true
				break
			}
		}
		if !found {
			target = append(target, item)
		}
	}
	return target
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isShellExitStatus(err error) bool {
	var exitErr *shellExitError
	return errors.As(err, &exitErr)
}

func (e *Engine) getDeps(target config.Target) []string {
	if !e.project.SecondExpansion {
		return target.Deps
	}
	key := "deps:" + target.Name
	if cached, ok := e.expandedCache[key]; ok {
		return cached
	}
	auto := e.automaticContextOnlyTarget(target)
	res := e.project.ExpandPrerequisites(target.Deps, auto)
	e.expandedCache[key] = res
	return res
}

func (e *Engine) getOrderOnlyDeps(target config.Target) []string {
	if !e.project.SecondExpansion {
		return target.OrderOnlyDeps
	}
	key := "orderonly:" + target.Name
	if cached, ok := e.expandedCache[key]; ok {
		return cached
	}
	auto := e.automaticContextOnlyTarget(target)
	res := e.project.ExpandPrerequisites(target.OrderOnlyDeps, auto)
	e.expandedCache[key] = res
	return res
}

func (e *Engine) getAllPrerequisites(target config.Target) []string {
	deps := e.getDeps(target)
	oodeps := e.getOrderOnlyDeps(target)
	res := make([]string, 0, len(deps)+len(oodeps))
	res = append(res, deps...)
	res = append(res, oodeps...)
	return res
}

func (e *Engine) automaticContextOnlyTarget(target config.Target) map[string]string {
	targetName := target.Name
	memberName := ""
	if archive, member, ok := parseArchiveTarget(target.Name); ok {
		targetName = archive
		memberName = member
	}

	values := map[string]string{
		"@":  targetName,
		"%":  memberName,
		"*":  targetStem(targetName),
		"@D": wordDirs(targetName),
		"@F": wordFiles(targetName),
		"%D": wordDirs(memberName),
		"%F": wordFiles(memberName),
		"*D": wordDirs(targetStem(targetName)),
		"*F": wordFiles(targetStem(targetName)),
	}
	return values
}
