package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *parserState) expand(input string) string {
	if s.recursiveExpansionErr != nil {
		return ""
	}
	if !s.warnUndefined {
		return expandVarsWithStackCallbacks(input, s.project.Variables, s.varFlavors, nil, nil, s.reportRecursiveExpansion)
	}
	return expandVarsWithStackCallbacks(input, s.project.Variables, s.varFlavors, nil, s.warnUndefinedVariable, s.reportRecursiveExpansion)
}

func (s *parserState) expandRecipe(input string) string {
	if s.recursiveExpansionErr != nil {
		return ""
	}
	vars, flavors := s.recipeExpansionState()
	if !s.warnUndefined {
		return expandRecipeVarsWithCallbacks(input, vars, flavors, nil, s.reportRecursiveExpansion)
	}
	return expandRecipeVarsWithCallbacks(input, vars, flavors, s.warnUndefinedVariable, s.reportRecursiveExpansion)
}

func (s *parserState) expandRecipeForTarget(targetName, input string) string {
	if s.recursiveExpansionErr != nil {
		return ""
	}
	vars, flavors := s.recipeExpansionStateForTarget(targetName)
	if !s.warnUndefined {
		return expandRecipeVarsWithCallbacks(input, vars, flavors, nil, s.reportRecursiveExpansion)
	}
	return expandRecipeVarsWithCallbacks(input, vars, flavors, s.warnUndefinedVariable, s.reportRecursiveExpansion)
}

func (s *parserState) recipeExpansionState() (map[string]string, map[string]VariableFlavor) {
	if len(s.privateGlobals) == 0 {
		return s.project.Variables, s.varFlavors
	}
	vars := make(map[string]string, len(s.project.Variables))
	for key, value := range s.project.Variables {
		if s.isPrivateVariableKey(key) {
			continue
		}
		vars[key] = value
	}
	flavors := make(map[string]VariableFlavor, len(s.varFlavors))
	for key, flavor := range s.varFlavors {
		if s.privateGlobals[key] {
			continue
		}
		flavors[key] = flavor
	}
	return vars, flavors
}

func (s *parserState) recipeExpansionStateForTarget(targetName string) (map[string]string, map[string]VariableFlavor) {
	patternVars, patternFlavors := s.patternExpansionStateForTarget(targetName)
	targetVars := s.targetVars[targetName]
	targetFlavors := s.targetFlavors[targetName]
	if len(patternVars) == 0 && len(patternFlavors) == 0 && len(targetVars) == 0 && len(targetFlavors) == 0 {
		return s.recipeExpansionState()
	}

	baseVars, baseFlavors := s.recipeExpansionState()
	vars := make(map[string]string, len(baseVars)+len(patternVars)+len(targetVars))
	for key, value := range baseVars {
		vars[key] = value
	}
	for key, value := range patternVars {
		vars[key] = value
	}
	for key, value := range targetVars {
		vars[key] = value
	}

	flavors := make(map[string]VariableFlavor, len(baseFlavors)+len(patternFlavors)+len(targetFlavors))
	for key, flavor := range baseFlavors {
		flavors[key] = flavor
	}
	for key, flavor := range patternFlavors {
		flavors[key] = flavor
	}
	for key, flavor := range targetFlavors {
		flavors[key] = flavor
	}
	return vars, flavors
}

func (s *parserState) patternExpansionStateForTarget(targetName string) (map[string]string, map[string]VariableFlavor) {
	if len(s.patternOrder) == 0 {
		return nil, nil
	}
	type matchedPattern struct {
		pattern string
		stemLen int
		order   int
	}
	matches := make([]matchedPattern, 0, len(s.patternOrder))
	for order, pattern := range s.patternOrder {
		stemLen, ok := patternStemLength(pattern, targetName)
		if !ok {
			continue
		}
		matches = append(matches, matchedPattern{pattern: pattern, stemLen: stemLen, order: order})
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].stemLen != matches[j].stemLen {
			return matches[i].stemLen > matches[j].stemLen
		}
		return matches[i].order < matches[j].order
	})

	var vars map[string]string
	var flavors map[string]VariableFlavor
	for _, match := range matches {
		if vars == nil {
			vars = map[string]string{}
			flavors = map[string]VariableFlavor{}
		}
		for key, value := range s.patternVars[match.pattern] {
			vars[key] = value
		}
		for key, flavor := range s.patternFlavors[match.pattern] {
			flavors[key] = flavor
		}
	}
	return vars, flavors
}

func patternStemLength(pattern, target string) (int, bool) {
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		if pattern != target {
			return 0, false
		}
		return 0, true
	}
	prefix := pattern[:idx]
	suffix := pattern[idx+1:]
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, suffix) {
		return 0, false
	}
	if len(target) < len(prefix)+len(suffix) {
		return 0, false
	}
	return len(target) - len(prefix) - len(suffix), true
}

func (s *parserState) isPrivateVariableKey(key string) bool {
	if s.privateGlobals[key] {
		return true
	}
	if strings.HasPrefix(key, originPrefix) {
		_, ok := s.privateGlobals[strings.TrimPrefix(key, originPrefix)]
		return ok
	}
	return false
}

func (s *parserState) warnUndefinedVariable(name string) {
	if name == "" || strings.HasPrefix(name, originPrefix) {
		return
	}
	if s.warnedUndefined[name] {
		return
	}
	s.warnedUndefined[name] = true
	fmt.Fprintf(os.Stderr, "warning: undefined variable %s\n", name)
}

func (s *parserState) reportRecursiveExpansion(name string) {
	if s.recursiveExpansionErr != nil {
		return
	}
	if name == "" || strings.HasPrefix(name, originPrefix) {
		return
	}
	s.recursiveExpansionErr = fmt.Errorf("recursive variable %q references itself (eventually)", name)
}

func (s *parserState) loadFile(path string) error {
	s.addMakefile(path)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, rawLine := range preprocessLines(string(content)) {
		if err := s.parseRawLine(path, rawLine); err != nil {
			return err
		}
	}
	if s.recursiveExpansionErr != nil {
		return s.recursiveExpansionErr
	}
	if s.define != nil {
		return errors.New("unterminated define block")
	}
	return nil
}

func (s *parserState) parseRawLine(path, rawLine string) error {
	if s.recursiveExpansionErr != nil {
		return s.recursiveExpansionErr
	}
	if s.define != nil {
		line := strings.TrimSpace(stripComment(rawLine))
		if line == "endef" {
			return s.finishDefine()
		}
		s.define.lines = append(s.define.lines, rawLine)
		return nil
	}

	if isRecipeLine(rawLine, s.recipePrefix) {
		if !s.isActive() {
			return nil
		}
		command, err := parseRecipeCommand(rawLine, s.recipePrefix)
		if err != nil {
			return err
		}
		rawCommandText := command.Text
		for _, target := range s.current {
			targetCommand := command
			targetCommand.Text = rawCommandText
			target.Commands = append(target.Commands, targetCommand)
		}
		return nil
	}

	strippedLine := stripComment(rawLine)
	line := strings.TrimSpace(strippedLine)
	lineForAssignment := strings.TrimLeft(strippedLine, " \t")
	if line == "" {
		return nil
	}
	if shouldRejectSpaceIndentedLine(rawLine, line) {
		return errors.New("missing separator (recipe lines must start with a tab)")
	}

	handled, err := s.handleConditional(line)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	if !s.isActive() {
		return nil
	}

	if handled, err := s.handleTopLevelEvalLine(path, line); err != nil {
		return err
	} else if handled {
		return nil
	}

	if strings.HasPrefix(line, "$(") || strings.HasPrefix(line, "${") {
		expandedLine := stripComment(s.expand(line))
		line = strings.TrimSpace(expandedLine)
		lineForAssignment = strings.TrimLeft(expandedLine, " \t")
		if s.recursiveExpansionErr != nil {
			return s.recursiveExpansionErr
		}
		if line == "" {
			return nil
		}
	}

	if line == "endef" {
		return errors.New("endef without matching define")
	}
	if block, ok, err := parseDefineDirective(line); err != nil {
		return err
	} else if ok {
		s.current = nil
		s.define = block
		return nil
	}

	switch {
	case line == ".ONESHELL:":
		s.project.OneShell = true
		return nil
	case line == ".SECONDEXPANSION:":
		s.project.SecondExpansion = true
		return nil
	case strings.HasPrefix(line, ".NOTPARALLEL:"):
		s.project.NotParallel = true
		return nil
	case line == ".EXPORT_ALL_VARIABLES:":
		s.exportAll = true
		return nil
	case strings.HasPrefix(line, ".PHONY:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".PHONY:"))))
		for _, name := range names {
			s.phony[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".SILENT:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".SILENT:"))))
		if len(names) == 0 {
			s.silentAll = true
			return nil
		}
		for _, name := range names {
			s.silentTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".IGNORE:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".IGNORE:"))))
		if len(names) == 0 {
			s.ignoreAll = true
			return nil
		}
		for _, name := range names {
			s.ignoreTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".PRECIOUS:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".PRECIOUS:"))))
		if len(names) == 0 {
			s.preciousAll = true
			return nil
		}
		for _, name := range names {
			s.preciousTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".INTERMEDIATE:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".INTERMEDIATE:"))))
		for _, name := range names {
			s.intermediateTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".NOTINTERMEDIATE:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".NOTINTERMEDIATE:"))))
		if len(names) == 0 {
			s.notIntermediateAll = true
			return nil
		}
		for _, name := range names {
			s.notIntermediateTargets[name] = true
		}
		return nil
	case line == ".POSIX:":
		s.posix = true
		// POSIX mode implies environment overrides
		s.envOverride = true
		return nil
	case strings.HasPrefix(line, ".LOW_RESOLUTION_TIME:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".LOW_RESOLUTION_TIME:"))))
		if len(names) == 0 {
			s.lowResolutionTimeAll = true
			return nil
		}
		for _, name := range names {
			s.lowResolutionTimeTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".SECONDARY:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".SECONDARY:"))))
		if len(names) == 0 {
			s.secondaryAll = true
			return nil
		}
		for _, name := range names {
			s.secondaryTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".DELETE_ON_ERROR:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".DELETE_ON_ERROR:"))))
		if len(names) == 0 {
			s.deleteOnErrorAll = true
			return nil
		}
		for _, name := range names {
			s.deleteOnErrorTargets[name] = true
		}
		return nil
	case strings.HasPrefix(line, ".SUFFIXES:"):
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, ".SUFFIXES:"))))
		if len(names) == 0 {
			s.suffixes = nil
		} else {
			s.suffixes = appendUnique(s.suffixes, names...)
		}
		return nil
	}

	if handled, err := s.handleVPathDirective(line); err != nil {
		return err
	} else if handled {
		return nil
	}

	if handled, err := s.handleTargetSpecificAssignment(line); err != nil {
		return err
	} else if handled {
		return nil
	}

	if err := rejectUnsupportedSyntax(line); err != nil {
		return err
	}
	s.current = nil

	if handled, err := s.handleExportDirectives(line); err != nil {
		return err
	} else if handled {
		return nil
	}

	if strings.HasPrefix(line, "override ") {
		return s.handleOverrideDirective(strings.TrimSpace(strings.TrimPrefix(line, "override ")))
	}
	if hasDirectiveArg(line, "private") {
		return s.handlePrivateDirective(directiveArg(line, "private"))
	}
	if hasDirectiveArg(line, "undefine") {
		return s.handleUndefineDirective(directiveArg(line, "undefine"))
	}

	if isIncludeDirective(line) {
		return s.handleInclude(path, line)
	}

	if key, value, operator, ok := parseAssignment(lineForAssignment); ok {
		return s.applyFileAssignment(key, value, operator)
	}

	colon := strings.Index(line, ":")
	if colon <= 0 {
		return nil
	}

	leftRaw := strings.TrimSpace(line[:colon])
	isGrouped := false
	if strings.HasSuffix(leftRaw, "&") {
		isGrouped = true
		leftRaw = strings.TrimSpace(leftRaw[:len(leftRaw)-1])
	}
	rightRaw := strings.TrimSpace(line[colon+1:])
	isDoubleColon := false
	if strings.HasPrefix(rightRaw, ":") {
		isDoubleColon = true
		rightRaw = strings.TrimSpace(rightRaw[1:])
	}
	if isDoubleColon && isGrouped {
		return errors.New("grouped targets cannot use double-colon syntax")
	}

	// Handle static pattern rule: targets ... : target-pattern : prereq-patterns ...
	if secondColon := strings.Index(rightRaw, ":"); secondColon >= 0 {
		if isDoubleColon {
			return errors.New("static pattern rules cannot use double-colon syntax")
		}
		targetPatternRaw := strings.TrimSpace(rightRaw[:secondColon])
		prereqPatternsRaw := strings.TrimSpace(rightRaw[secondColon+1:])

		targetNames := splitFields(s.expand(leftRaw))
		targetPattern := s.expand(targetPatternRaw)
		prereqPatterns, inlineRecipe := splitInlineRecipe(prereqPatternsRaw)
		prereqPatterns = s.expand(prereqPatterns)

		depsTemplate, orderOnlyTemplate := splitPrerequisites(prereqPatterns)

		for _, name := range targetNames {
			stem, ok := patternStem(targetPattern, name)
			if !ok {
				return fmt.Errorf("target %q does not match static pattern %q", name, targetPattern)
			}

			target := s.ensureTarget(name)
			concreteDeps := expandTemplate(depsTemplate, stem)
			concreteOrderOnly := expandTemplate(orderOnlyTemplate, stem)
			target.Deps = appendUnique(target.Deps, concreteDeps...)
			target.OrderOnlyDeps = appendUnique(target.OrderOnlyDeps, concreteOrderOnly...)
			s.current = append(s.current, target)

			for _, dep := range concreteDeps {
				s.ensureTarget(dep)
			}
			for _, dep := range concreteOrderOnly {
				s.ensureTarget(dep)
			}
		}

		if inlineRecipe != "" {
			command, err := parseInlineRecipeCommand(inlineRecipe)
			if err != nil {
				return err
			}
			rawCommandText := command.Text
			for _, target := range s.current {
				targetCommand := command
				targetCommand.Text = rawCommandText
				target.Commands = append(target.Commands, targetCommand)
			}
		}
		return nil
	}

	left := s.expand(leftRaw)
	rawDeps, inlineRecipe := splitInlineRecipe(rightRaw)
	right := s.expand(rawDeps)
	targetNames := splitFields(left)
	deps, orderOnly := splitPrerequisites(right)
	
	var groupTargets []string
	if isGrouped && len(targetNames) > 1 {
		groupTargets = append([]string(nil), targetNames...)
	}

	for _, name := range targetNames {
		if strings.Contains(name, "%") {
			if isDoubleColon {
				return errors.New("pattern rules cannot use double-colon syntax")
			}
			target := &Target{
				Name:          name,
				Deps:          append([]string(nil), deps...),
				OrderOnlyDeps: append([]string(nil), orderOnly...),
				GroupTargets:  groupTargets,
			}
			s.patternRules = append(s.patternRules, target)
			s.current = append(s.current, target)
			continue
		}

		if isDoubleColon {
			if existing := s.targets[name]; existing != nil && existing.Explicit && !existing.DoubleColon {
				return fmt.Errorf("target %q already exists as a single-colon rule", name)
			}
			target := &Target{
				Name:          name,
				DoubleColon:   true,
				Explicit:      true,
				Deps:          append([]string(nil), deps...),
				OrderOnlyDeps: append([]string(nil), orderOnly...),
			}
			s.order = append(s.order, name)
			if s.targets[name] == nil {
				s.targets[name] = target
			} else {
				s.targets[name].DoubleColon = true
				s.targets[name].Explicit = true
			}
			s.allDoubleColon = append(s.allDoubleColon, target)
			s.current = append(s.current, target)
		} else {
			if existing := s.targets[name]; existing != nil && existing.Explicit && existing.DoubleColon {
				return fmt.Errorf("target %q already exists as a double-colon rule", name)
			}
			target := s.ensureTarget(name)
			target.Deps = appendUnique(target.Deps, deps...)
			target.OrderOnlyDeps = appendUnique(target.OrderOnlyDeps, orderOnly...)
			if isGrouped && len(groupTargets) > 1 {
				target.GroupTargets = groupTargets
			}
			s.current = append(s.current, target)
		}
	}
	for _, dep := range deps {
		if !strings.Contains(dep, "%") {
			s.ensureTargetEntry(dep)
		}
	}
	for _, dep := range orderOnly {
		if !strings.Contains(dep, "%") {
			s.ensureTargetEntry(dep)
		}
	}
	if inlineRecipe == "" {
		return nil
	}

	command, err := parseInlineRecipeCommand(inlineRecipe)
	if err != nil {
		return err
	}
	rawCommandText := command.Text
	for _, target := range s.current {
		targetCommand := command
		targetCommand.Text = rawCommandText
		target.Commands = append(target.Commands, targetCommand)
	}
	return nil
}

func patternStem(pattern, name string) (string, bool) {
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		if pattern == name {
			return "", true
		}
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

func expandTemplate(templates []string, stem string) []string {
	out := make([]string, len(templates))
	for i, template := range templates {
		out[i] = strings.ReplaceAll(template, "%", stem)
	}
	return out
}

func (s *parserState) handleTopLevelEvalLine(path, line string) (bool, error) {
	args, ok := parseTopLevelEvalInvocation(line)
	if !ok {
		return false, nil
	}

	var text string
	if !s.warnUndefined {
		text = expandVarsWithStackCallbacks(args, s.project.Variables, s.varFlavors, nil, nil, s.reportRecursiveExpansion)
	} else {
		text = expandVarsWithStackCallbacks(args, s.project.Variables, s.varFlavors, nil, s.warnUndefinedVariable, s.reportRecursiveExpansion)
	}
	if s.recursiveExpansionErr != nil {
		return true, s.recursiveExpansionErr
	}
	for _, generatedLine := range preprocessLines(text) {
		if err := s.parseRawLine(path, generatedLine); err != nil {
			return true, err
		}
	}
	return true, nil
}

func parseTopLevelEvalInvocation(line string) (string, bool) {
	if !strings.HasPrefix(line, "$(") && !strings.HasPrefix(line, "${") {
		return "", false
	}
	start, end, kind, ok := findVarRef(line, false)
	if !ok || start != 0 || end != len(line) {
		return "", false
	}
	if kind != '(' && kind != '{' {
		return "", false
	}
	expr := strings.TrimSpace(line[2 : len(line)-1])
	name, args, ok := splitFunctionInvocation(expr)
	if !ok || name != "eval" {
		return "", false
	}
	return args, true
}

func parseDefineDirective(line string) (*defineBlock, bool, error) {
	if !strings.HasPrefix(line, "define") {
		return nil, false, nil
	}
	if len(line) > len("define") {
		next := line[len("define")]
		if next != ' ' && next != '\t' {
			return nil, false, nil
		}
	}
	args := strings.TrimSpace(line[len("define"):])
	if args == "" {
		return nil, true, errors.New("define directive requires a variable name")
	}

	if key, value, operator, ok := parseAssignment(args); ok {
		if strings.TrimSpace(value) != "" {
			return nil, true, errors.New("define assignment must not include inline value")
		}
		return &defineBlock{name: key, operator: operator}, true, nil
	}
	if strings.ContainsAny(args, " \t") {
		return nil, true, errors.New("define directive has invalid variable name")
	}
	return &defineBlock{name: args, operator: "="}, true, nil
}

func (s *parserState) finishDefine() error {
	block := s.define
	s.define = nil
	if block == nil {
		return errors.New("endef without matching define")
	}
	if s.overrides[block.name] {
		return nil
	}
	if s.envOverride && s.envImported[block.name] {
		return nil
	}
	if block.operator == "" {
		block.operator = "="
	}
	value := strings.Join(block.lines, "\n")
	if err := applyAssignmentWithWarning(s.project.Variables, s.varFlavors, block.name, value, block.operator, s.warnUndefinedVariable); err != nil {
		return err
	}
	setVariableOrigin(s.project.Variables, block.name, "file")
	if block.name == ".RECIPEPREFIX" {
		s.updateRecipePrefix()
	}
	return nil
}

func (s *parserState) handleOverrideDirective(body string) error {
	key, value, operator, ok := parseAssignment(body)
	if !ok {
		return errors.New("override directive requires variable assignment")
	}
	if err := applyAssignmentWithWarning(s.project.Variables, s.varFlavors, key, value, operator, s.warnUndefinedVariable); err != nil {
		return err
	}
	s.overrideSet[key] = true
	setVariableOrigin(s.project.Variables, key, "override")
	if key == ".RECIPEPREFIX" {
		s.updateRecipePrefix()
	}
	return nil
}

func (s *parserState) handlePrivateDirective(body string) error {
	key, value, operator, ok := parseAssignment(strings.TrimSpace(body))
	if !ok {
		return errors.New("private directive requires variable assignment")
	}
	s.privateGlobals[key] = true
	return s.applyFileAssignment(key, value, operator)
}

func (s *parserState) handleUndefineDirective(body string) error {
	names := splitFields(s.expand(strings.TrimSpace(body)))
	for _, name := range names {
		delete(s.project.Variables, name)
		delete(s.varFlavors, name)
		delete(s.overrides, name)
		delete(s.overrideSet, name)
		delete(s.privateGlobals, name)
		delete(s.envImported, name)
		delete(s.exported, name)
		delete(s.unexported, name)
		delete(s.project.Variables, originPrefix+name)
	}
	return nil
}

func (s *parserState) handleVPathDirective(line string) (bool, error) {
	if line == "vpath" {
		s.project.VPaths = nil
		return true, nil
	}
	if !hasDirectiveArg(line, "vpath") {
		return false, nil
	}
	body := s.expand(directiveArg(line, "vpath"))
	fields := splitFields(body)
	if len(fields) == 0 {
		s.project.VPaths = nil
		return true, nil
	}
	pattern := fields[0]
	if len(fields) == 1 {
		s.removeVPathPattern(pattern)
		return true, nil
	}
	directories := parseVPathDirectories(fields[1:])
	if len(directories) == 0 {
		s.removeVPathPattern(pattern)
		return true, nil
	}
	s.removeVPathPattern(pattern)
	s.project.VPaths = append(s.project.VPaths, VPath{Pattern: pattern, Directories: append([]string(nil), directories...)})
	return true, nil
}

func parseVPathDirectories(parts []string) []string {
	sep := string(os.PathListSeparator)
	var directories []string
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

func (s *parserState) removeVPathPattern(pattern string) {
	filtered := s.project.VPaths[:0]
	for _, entry := range s.project.VPaths {
		if entry.Pattern == pattern {
			continue
		}
		filtered = append(filtered, entry)
	}
	s.project.VPaths = filtered
}

func (s *parserState) applyFileAssignment(key, value, operator string) error {
	if s.overrides[key] {
		return nil
	}
	if s.overrideSet[key] {
		return nil
	}
	if s.envOverride && s.envImported[key] {
		return nil
	}
	if err := applyAssignmentWithWarning(s.project.Variables, s.varFlavors, key, value, operator, s.warnUndefinedVariable); err != nil {
		return err
	}
	setVariableOrigin(s.project.Variables, key, "file")
	if key == ".RECIPEPREFIX" {
		s.updateRecipePrefix()
	}
	return nil
}

func (s *parserState) handleTargetSpecificAssignment(line string) (bool, error) {
	colon := strings.Index(line, ":")
	if colon <= 0 {
		return false, nil
	}
	left := s.expand(strings.TrimSpace(line[:colon]))
	targetNames := splitFields(left)
	if len(targetNames) == 0 {
		return false, nil
	}

	body, useOverride, isPrivate := parseTargetSpecificAssignmentBody(strings.TrimSpace(line[colon+1:]))
	if startsWithAssignmentOperator(body) {
		return false, nil
	}
	if body == "" {
		return false, nil
	}

	key, value, operator, ok := parseAssignment(body)
	if !ok || strings.Contains(key, ":") {
		return false, nil
	}

	hasPattern := false
	hasExplicit := false
	for _, name := range targetNames {
		if strings.Contains(name, "%") {
			hasPattern = true
			continue
		}
		hasExplicit = true
	}
	if hasPattern && hasExplicit {
		return true, errors.New("mixed explicit and pattern-specific variable assignments are not supported yet")
	}
	if hasPattern {
		s.current = nil
		for _, pattern := range targetNames {
			if err := s.applyPatternAssignment(pattern, key, value, operator, useOverride, isPrivate); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	s.current = nil
	for _, name := range targetNames {
		target := s.ensureTarget(name)
		if err := s.applyTargetAssignment(name, key, value, operator, useOverride, isPrivate); err != nil {
			return true, err
		}
		s.current = append(s.current, target)
	}
	return true, nil
}

func (s *parserState) applyTargetAssignment(targetName, key, value, operator string, useOverride, isPrivate bool) error {
	targetVars, targetFlavors, targetOverrideSet, targetPrivateVars := s.ensureTargetAssignmentState(targetName)
	if targetOverrideSet[key] && !useOverride {
		return nil
	}
	if s.overrides[key] && !useOverride {
		if overrideValue, ok := s.project.Variables[key]; ok {
			targetVars[key] = overrideValue
			targetFlavors[key] = s.varFlavors[key]
			if origin := getVariableOrigin(s.project.Variables, key); origin != "" {
				targetVars[originPrefix+key] = origin
				targetFlavors[originPrefix+key] = FlavorSimple
			}
		}
		return nil
	}
	patternVars, patternFlavors := s.patternExpansionStateForTarget(targetName)

	mergedVars := make(map[string]string, len(s.project.Variables)+len(patternVars)+len(targetVars))
	for name, current := range s.project.Variables {
		mergedVars[name] = current
	}
	for name, current := range patternVars {
		mergedVars[name] = current
	}
	for name, current := range targetVars {
		mergedVars[name] = current
	}
	mergedFlavors := make(map[string]VariableFlavor, len(s.varFlavors)+len(patternFlavors)+len(targetFlavors))
	for name, flavor := range s.varFlavors {
		mergedFlavors[name] = flavor
	}
	for name, flavor := range patternFlavors {
		mergedFlavors[name] = flavor
	}
	for name, flavor := range targetFlavors {
		mergedFlavors[name] = flavor
	}

	// For += we need to check if it exists in target scope, if not, check global scope
	if operator == "+=" {
		if _, ok := targetVars[key]; !ok {
			if globalVal, ok := s.project.Variables[key]; ok {
				mergedVars[key] = globalVal
				mergedFlavors[key] = s.varFlavors[key]
			}
		}
	}

	_, existed := mergedVars[key]
	if err := applyAssignmentWithWarning(mergedVars, mergedFlavors, key, value, operator, s.warnUndefinedVariable); err != nil {
		return err
	}
	if operator == "?=" && existed {
		return nil
	}
	targetVars[key] = mergedVars[key]
	targetFlavors[key] = mergedFlavors[key]
	origin := "file"
	if useOverride {
		origin = "override"
	}
	targetVars[originPrefix+key] = origin
	targetFlavors[originPrefix+key] = FlavorSimple
	if useOverride {
		targetOverrideSet[key] = true
	}
	if isPrivate {
		targetPrivateVars[key] = true
	}
	return nil
}

func (s *parserState) applyPatternAssignment(pattern, key, value, operator string, useOverride, isPrivate bool) error {
	patternVars, patternFlavors, patternOverrideSet, patternPrivateVars := s.ensurePattern(pattern)
	if patternOverrideSet[key] && !useOverride {
		return nil
	}
	if s.overrides[key] && !useOverride {
		return s.applyPatternAssignmentUnderCommandLineOverride(patternVars, patternFlavors, key, operator)
	}

	mergedVars := make(map[string]string, len(s.project.Variables)+len(patternVars))
	for name, current := range s.project.Variables {
		mergedVars[name] = current
	}
	for name, current := range patternVars {
		mergedVars[name] = current
	}
	mergedFlavors := make(map[string]VariableFlavor, len(s.varFlavors)+len(patternFlavors))
	for name, flavor := range s.varFlavors {
		mergedFlavors[name] = flavor
	}
	for name, flavor := range patternFlavors {
		mergedFlavors[name] = flavor
	}

	_, existed := mergedVars[key]
	if err := applyAssignmentWithWarning(mergedVars, mergedFlavors, key, value, operator, s.warnUndefinedVariable); err != nil {
		return err
	}
	if operator == "?=" && existed {
		return nil
	}
	patternVars[key] = mergedVars[key]
	patternFlavors[key] = mergedFlavors[key]
	origin := "file"
	if useOverride {
		origin = "override"
	}
	patternVars[originPrefix+key] = origin
	patternFlavors[originPrefix+key] = FlavorSimple
	if useOverride {
		patternOverrideSet[key] = true
	}
	if isPrivate {
		patternPrivateVars[key] = true
	}
	return nil
}

func (s *parserState) applyPatternAssignmentUnderCommandLineOverride(patternVars map[string]string, patternFlavors map[string]VariableFlavor, key, operator string) error {
	base, ok := s.project.Variables[key]
	if !ok {
		return nil
	}
	origin := getVariableOrigin(s.project.Variables, key)
	flavor := s.varFlavors[key]

	switch operator {
	case "+=":
		if current, exists := patternVars[key]; exists && current != "" {
			if base != "" {
				patternVars[key] = current + " " + base
			}
		} else {
			patternVars[key] = base
		}
		patternFlavors[key] = flavor
		if origin != "" {
			patternVars[originPrefix+key] = origin
			patternFlavors[originPrefix+key] = FlavorSimple
		}
		patternVars[patternCommandLineAppendBasePrefix+key] = base
		patternFlavors[patternCommandLineAppendBasePrefix+key] = FlavorSimple
		patternVars[patternCommandLineAppendSeedPrefix+key] = patternVars[key]
		patternFlavors[patternCommandLineAppendSeedPrefix+key] = FlavorSimple
	case "?=":
		return nil
	default:
		delete(patternVars, key)
		delete(patternFlavors, key)
		delete(patternVars, originPrefix+key)
		delete(patternFlavors, originPrefix+key)
		delete(patternVars, patternCommandLineAppendBasePrefix+key)
		delete(patternFlavors, patternCommandLineAppendBasePrefix+key)
		delete(patternVars, patternCommandLineAppendSeedPrefix+key)
		delete(patternFlavors, patternCommandLineAppendSeedPrefix+key)
	}
	return nil
}

func (s *parserState) ensureTargetAssignmentState(targetName string) (map[string]string, map[string]VariableFlavor, map[string]bool, map[string]bool) {
	targetVars, ok := s.targetVars[targetName]
	if !ok {
		targetVars = map[string]string{}
		s.targetVars[targetName] = targetVars
	}
	targetFlavors, ok := s.targetFlavors[targetName]
	if !ok {
		targetFlavors = map[string]VariableFlavor{}
		s.targetFlavors[targetName] = targetFlavors
	}
	targetOverrideSet, ok := s.targetOverrideSet[targetName]
	if !ok {
		targetOverrideSet = map[string]bool{}
		s.targetOverrideSet[targetName] = targetOverrideSet
	}
	targetPrivateVars, ok := s.targetPrivateVars[targetName]
	if !ok {
		targetPrivateVars = map[string]bool{}
		s.targetPrivateVars[targetName] = targetPrivateVars
	}
	return targetVars, targetFlavors, targetOverrideSet, targetPrivateVars
}

func (s *parserState) ensurePattern(pattern string) (map[string]string, map[string]VariableFlavor, map[string]bool, map[string]bool) {
	patternVars, ok := s.patternVars[pattern]
	if !ok {
		patternVars = map[string]string{}
		s.patternVars[pattern] = patternVars
		s.patternOrder = append(s.patternOrder, pattern)
	}
	patternFlavors, ok := s.patternFlavors[pattern]
	if !ok {
		patternFlavors = map[string]VariableFlavor{}
		s.patternFlavors[pattern] = patternFlavors
	}
	patternOverrideSet, ok := s.patternOverrideSet[pattern]
	if !ok {
		patternOverrideSet = map[string]bool{}
		s.patternOverrideSet[pattern] = patternOverrideSet
	}
	patternPrivateVars, ok := s.patternPrivateVars[pattern]
	if !ok {
		patternPrivateVars = map[string]bool{}
		s.patternPrivateVars[pattern] = patternPrivateVars
	}
	return patternVars, patternFlavors, patternOverrideSet, patternPrivateVars
}

func parseTargetSpecificAssignmentBody(body string) (string, bool, bool) {
	body = strings.TrimSpace(body)
	useOverride := false
	isPrivate := false
	for {
		switch {
		case hasDirectiveArg(body, "override"):
			body = strings.TrimSpace(directiveArg(body, "override"))
			useOverride = true
		case hasDirectiveArg(body, "private"):
			body = strings.TrimSpace(directiveArg(body, "private"))
			isPrivate = true
		default:
			return body, useOverride, isPrivate
		}
	}
}

func startsWithAssignmentOperator(body string) bool {
	return strings.HasPrefix(body, "::=") ||
		strings.HasPrefix(body, ":=") ||
		strings.HasPrefix(body, "?=") ||
		strings.HasPrefix(body, "+=") ||
		strings.HasPrefix(body, "!=") ||
		strings.HasPrefix(body, "=")
}

func (s *parserState) ensureTarget(name string) *Target {
	target := s.ensureTargetEntry(name)
	target.Explicit = true
	return target
}

func (s *parserState) ensureTargetEntry(name string) *Target {
	target := s.targets[name]
	if target != nil {
		return target
	}
	target = &Target{Name: name}
	s.targets[name] = target
	s.order = append(s.order, name)
	return target
}

func (s *parserState) prunePrivateGlobals() {
	if len(s.privateGlobals) == 0 {
		return
	}
	for name := range s.privateGlobals {
		delete(s.project.Variables, name)
		delete(s.project.Variables, originPrefix+name)
		delete(s.varFlavors, name)
		delete(s.exported, name)
		delete(s.unexported, name)
	}
}

func (s *parserState) handleExportDirectives(line string) (bool, error) {
	if line == "export" {
		s.exportAll = true
		return true, nil
	}
	if line == "unexport" {
		s.exportAll = false
		return true, nil
	}
	if strings.HasPrefix(line, "export ") {
		body := strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if key, value, operator, ok := parseAssignment(body); ok {
			if s.overrides[key] || s.overrideSet[key] || (s.envOverride && s.envImported[key]) {
				s.exported[key] = true
				delete(s.unexported, key)
				return true, nil
			}
			if err := applyAssignmentWithWarning(s.project.Variables, s.varFlavors, key, value, operator, s.warnUndefinedVariable); err != nil {
				return true, err
			}
			setVariableOrigin(s.project.Variables, key, "file")
			s.exported[key] = true
			delete(s.unexported, key)
			return true, nil
		}
		names := splitFields(s.expand(body))
		for _, name := range names {
			s.exported[name] = true
			delete(s.unexported, name)
		}
		return true, nil
	}
	if strings.HasPrefix(line, "unexport ") {
		names := splitFields(s.expand(strings.TrimSpace(strings.TrimPrefix(line, "unexport "))))
		for _, name := range names {
			s.unexported[name] = true
			delete(s.exported, name)
		}
		return true, nil
	}
	return false, nil
}

func (s *parserState) updateRecipePrefix() {
	prefix := resolveVar(".RECIPEPREFIX", s.project.Variables, s.varFlavors, nil, nil, nil)
	if prefix == "" {
		s.recipePrefix = '\t'
		return
	}
	s.recipePrefix = prefix[0]
}

func (s *parserState) addMakefile(path string) {
	list := s.project.Variables["MAKEFILE_LIST"]
	if list == "" {
		s.project.Variables["MAKEFILE_LIST"] = path
		return
	}
	s.project.Variables["MAKEFILE_LIST"] = list + " " + path
}

func (s *parserState) handleInclude(currentPath, line string) error {
	directive, remainder, optional := parseIncludeDirective(line)
	if directive == "" {
		return nil
	}

	names := splitFields(s.expand(remainder))
	baseDir := filepath.Dir(currentPath)
	for _, name := range names {
		includePath := name
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(baseDir, includePath)
		}
		if err := s.loadFile(includePath); err != nil {
			if optional && os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *parserState) handleConditional(line string) (bool, error) {
	switch {
	case hasDirectiveArg(line, "ifdef"):
		name := s.expand(directiveArg(line, "ifdef"))
		ok := s.expand("$("+name+")") != ""
		s.pushConditional(ok)
		return true, nil
	case hasDirectiveArg(line, "ifndef"):
		name := s.expand(directiveArg(line, "ifndef"))
		ok := s.expand("$("+name+")") == ""
		s.pushConditional(ok)
		return true, nil
	case hasConditionalExpr(line, "ifeq"):
		ok, err := evaluateIfEq(conditionalExpr(line, "ifeq"), s.project.Variables, s.varFlavors, true)
		if err != nil {
			return true, err
		}
		s.pushConditional(ok)
		return true, nil
	case hasConditionalExpr(line, "ifneq"):
		ok, err := evaluateIfEq(conditionalExpr(line, "ifneq"), s.project.Variables, s.varFlavors, false)
		if err != nil {
			return true, err
		}
		s.pushConditional(ok)
		return true, nil
	case strings.HasPrefix(line, "else"):
		if len(s.conditionals) == 0 {
			return true, errors.New("else without matching conditional")
		}
		frame := &s.conditionals[len(s.conditionals)-1]
		if frame.inElse {
			return true, errors.New("duplicate else in conditional block")
		}
		
		remainder := strings.TrimSpace(strings.TrimPrefix(line, "else"))
		if remainder == "" {
			frame.inElse = true
			frame.active = frame.parentActive && !frame.conditionMet
			return true, nil
		}
		
		// else if...
		if !frame.conditionMet {
			var ok bool
			var err error
			switch {
			case hasDirectiveArg(remainder, "ifdef"):
				name := s.expand(directiveArg(remainder, "ifdef"))
				ok = s.expand("$("+name+")") != ""
			case hasDirectiveArg(remainder, "ifndef"):
				name := s.expand(directiveArg(remainder, "ifndef"))
				ok = s.expand("$("+name+")") == ""
			case hasConditionalExpr(remainder, "ifeq"):
				ok, err = evaluateIfEq(conditionalExpr(remainder, "ifeq"), s.project.Variables, s.varFlavors, true)
			case hasConditionalExpr(remainder, "ifneq"):
				ok, err = evaluateIfEq(conditionalExpr(remainder, "ifneq"), s.project.Variables, s.varFlavors, false)
			default:
				return true, fmt.Errorf("invalid syntax after else: %q", remainder)
			}
			if err != nil {
				return true, err
			}
			frame.active = frame.parentActive && ok
			if ok {
				frame.conditionMet = true
			}
		} else {
			frame.active = false
		}
		return true, nil
	case line == "endif":
		if len(s.conditionals) == 0 {
			return true, errors.New("endif without matching conditional")
		}
		s.conditionals = s.conditionals[:len(s.conditionals)-1]
		return true, nil
	default:
		return false, nil
	}
}

func hasDirectiveArg(line, directive string) bool {
	if !strings.HasPrefix(line, directive) {
		return false
	}
	if len(line) == len(directive) {
		return false
	}
	next := line[len(directive)]
	return next == ' ' || next == '\t'
}

func directiveArg(line, directive string) string {
	return strings.TrimSpace(line[len(directive):])
}

func hasConditionalExpr(line, directive string) bool {
	if !strings.HasPrefix(line, directive) {
		return false
	}
	if len(line) == len(directive) {
		return false
	}
	next := line[len(directive)]
	switch next {
	case ' ', '\t', '(', '\'', '"':
		return true
	default:
		return false
	}
}

func conditionalExpr(line, directive string) string {
	return strings.TrimSpace(line[len(directive):])
}

func (s *parserState) pushConditional(condition bool) {
	parentActive := s.isActive()
	s.conditionals = append(s.conditionals, conditionalFrame{
		parentActive: parentActive,
		conditionMet: condition,
		active:       parentActive && condition,
	})
}

func (s *parserState) isActive() bool {
	if len(s.conditionals) == 0 {
		return true
	}
	return s.conditionals[len(s.conditionals)-1].active
}

func preprocessLines(content string) []string {
	lines := strings.Split(content, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			line = line[:len(line)-1]
			i++
			nextLine := lines[i]
			// GNU Make removes leading tab if it's a continued line
			if len(nextLine) > 0 && nextLine[0] == '\t' {
				nextLine = nextLine[1:]
			}
			line += " " + strings.TrimLeft(nextLine, " \t")
		}
		out = append(out, line)
	}
	return out
}

func isRecipeLine(line string, recipePrefix byte) bool {
	if len(line) == 0 {
		return false
	}
	return line[0] == recipePrefix
}

func shouldRejectSpaceIndentedLine(rawLine, line string) bool {
	if len(rawLine) == 0 || rawLine[0] != ' ' {
		return false
	}
	if line == "" {
		return false
	}
	if isIncludeDirective(line) || line == ".ONESHELL:" || line == ".EXPORT_ALL_VARIABLES:" || strings.HasPrefix(line, ".PHONY:") || strings.HasPrefix(line, ".SILENT:") || strings.HasPrefix(line, ".IGNORE:") || strings.HasPrefix(line, ".PRECIOUS:") || strings.HasPrefix(line, ".DELETE_ON_ERROR:") || strings.HasPrefix(line, "export") || strings.HasPrefix(line, "unexport") || strings.HasPrefix(line, "override") || strings.HasPrefix(line, "undefine") || strings.HasPrefix(line, "private") || strings.HasPrefix(line, "vpath") {
		return false
	}
	if _, _, _, ok := parseAssignment(line); ok {
		return false
	}
	if strings.Contains(line, ":") {
		return false
	}
	switch {
	case strings.HasPrefix(line, "ifdef "),
		strings.HasPrefix(line, "ifndef "),
		strings.HasPrefix(line, "ifeq "),
		strings.HasPrefix(line, "ifneq "),
		line == "else",
		line == "endif":
		return false
	}
	return true
}

func parseRecipeCommand(line string, recipePrefix byte) (RecipeCommand, error) {
	if len(line) == 0 || line[0] != recipePrefix {
		return RecipeCommand{}, errors.New("recipe command must begin with recipe prefix")
	}
	return parseRecipeCommandText(strings.TrimSpace(line[1:]))
}

func parseInlineRecipeCommand(line string) (RecipeCommand, error) {
	return parseRecipeCommandText(strings.TrimSpace(line))
}

func parseRecipeCommandText(text string) (RecipeCommand, error) {
	command := RecipeCommand{}
	for len(text) > 0 {
		switch text[0] {
		case '@':
			command.Silent = true
		case '-':
			command.IgnoreError = true
		case '+':
			command.Force = true
		default:
			command.Text = text
			if command.Text == "" {
				return RecipeCommand{}, errors.New("recipe command cannot be empty")
			}
			command.Recursive = containsRecursiveMakeReference(command.Text)
			return command, nil
		}
		text = strings.TrimSpace(text[1:])
	}
	return RecipeCommand{}, errors.New("recipe command cannot be empty")
}

func containsRecursiveMakeReference(text string) bool {
	return strings.Contains(text, "$(MAKE)") || strings.Contains(text, "${MAKE}")
}

func parseAssignment(line string) (key, value, operator string, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", "", false
	}

	opStart := eq
	operator = "="
	if eq >= 2 && line[eq-2] == ':' && line[eq-1] == ':' {
		opStart = eq - 2
		operator = "::="
	} else if eq >= 1 {
		switch line[eq-1] {
		case ':':
			opStart = eq - 1
			operator = ":="
		case '?':
			opStart = eq - 1
			operator = "?="
		case '+':
			opStart = eq - 1
			operator = "+="
		case '!':
			opStart = eq - 1
			operator = "!="
		}
	}

	key = strings.TrimSpace(line[:opStart])
	if key == "" {
		return "", "", "", false
	}
	value = strings.TrimLeft(line[eq+1:], " \t")
	return key, value, operator, true
}

func isIncludeDirective(line string) bool {
	return strings.HasPrefix(line, "include ") || strings.HasPrefix(line, "-include ") || strings.HasPrefix(line, "sinclude ")
}

func parseIncludeDirective(line string) (directive, remainder string, optional bool) {
	switch {
	case strings.HasPrefix(line, "-include "):
		return "-include", strings.TrimSpace(strings.TrimPrefix(line, "-include ")), true
	case strings.HasPrefix(line, "sinclude "):
		return "sinclude", strings.TrimSpace(strings.TrimPrefix(line, "sinclude ")), true
	case strings.HasPrefix(line, "include "):
		return "include", strings.TrimSpace(strings.TrimPrefix(line, "include ")), false
	default:
		return "", "", false
	}
}

func rejectUnsupportedSyntax(line string) error {
	if key, _, _, ok := parseAssignment(line); ok && !strings.Contains(key, ":") {
		return nil
	}
	switch {
	case strings.HasPrefix(line, "export "),
		strings.HasPrefix(line, "unexport "):
		return nil
	}
	return nil
}

func stripComment(line string) string {
	escaped := false
	parenDepth := 0
	braceDepth := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			escaped = !escaped
			continue
		case '$':
			if escaped {
				escaped = false
				continue
			}
			if i+1 < len(line) {
				switch line[i+1] {
				case '(':
					parenDepth++
					i++
					continue
				case '{':
					braceDepth++
					i++
					continue
				}
			}
			escaped = false
			continue
		case '(':
			if escaped {
				escaped = false
				continue
			}
			if parenDepth > 0 {
				parenDepth++
			}
			escaped = false
			continue
		case ')':
			if escaped {
				escaped = false
				continue
			}
			if parenDepth > 0 {
				parenDepth--
			}
			escaped = false
			continue
		case '{':
			if escaped {
				escaped = false
				continue
			}
			if braceDepth > 0 {
				braceDepth++
			}
			escaped = false
			continue
		case '}':
			if escaped {
				escaped = false
				continue
			}
			if braceDepth > 0 {
				braceDepth--
			}
			escaped = false
			continue
		case '#':
			if !escaped && parenDepth == 0 && braceDepth == 0 {
				return line[:i]
			}
			escaped = false
		default:
			escaped = false
		}
	}
	return line
}

func splitPrerequisites(input string) ([]string, []string) {
	parts := strings.SplitN(input, "|", 2)
	deps := splitFields(strings.TrimSpace(parts[0]))
	if len(parts) == 1 {
		return deps, nil
	}
	return deps, splitFields(strings.TrimSpace(parts[1]))
}

func splitInlineRecipe(input string) (string, string) {
	idx := strings.Index(input, ";")
	if idx < 0 {
		return strings.TrimSpace(input), ""
	}
	deps := strings.TrimSpace(input[:idx])
	recipe := strings.TrimSpace(input[idx+1:])
	return deps, recipe
}

func splitFields(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	isSpace := func(ch byte) bool {
		switch ch {
		case ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	}

	fields := make([]string, 0, 4)
	parenDepth := 0
	braceDepth := 0
	for idx := 0; idx < len(input); {
		for idx < len(input) && isSpace(input[idx]) {
			idx++
		}
		if idx >= len(input) {
			break
		}

		var field strings.Builder
		for idx < len(input) {
			ch := input[idx]
			if isSpace(ch) && parenDepth == 0 && braceDepth == 0 {
				break
			}

			if ch == '\\' && idx+1 < len(input) {
				field.WriteByte(input[idx+1])
				idx += 2
				continue
			}
			if ch == '\\' {
				field.WriteByte('\\')
				idx++
				continue
			}

			if ch == '(' {
				parenDepth++
			} else if ch == ')' {
				if parenDepth > 0 {
					parenDepth--
				}
			} else if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				if braceDepth > 0 {
					braceDepth--
				}
			}

			field.WriteByte(ch)
			idx++
		}
		fields = append(fields, field.String())
	}

	return fields
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}
