package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Load(path string) (*Project, error) {
	return LoadWithContext(path, LoadContext{})
}

func LoadWithOverrides(path string, overrides map[string]string) (*Project, error) {
	return LoadWithContext(path, LoadContext{Overrides: overrides})
}

type LoadContext struct {
	Overrides           map[string]string
	Goals               []string
	MakeProgram         string
	MakeFlags           string
	EnvironmentOverride bool
	WarnUndefined       bool
	NoBuiltinRules      bool
	NoBuiltinVars       bool
}

func LoadWithContext(path string, ctx LoadContext) (*Project, error) {
	resolved, format, err := resolveInput(path)
	if err != nil {
		return nil, err
	}

	state := &parserState{
		project: &Project{
			Variables: map[string]string{},
			Format:    format,
		},
		phony:                  map[string]bool{},
		targets:                map[string]*Target{},
		varFlavors:             map[string]VariableFlavor{},
		overrides:              map[string]bool{},
		overrideSet:            map[string]bool{},
		targetVars:             map[string]map[string]string{},
		targetFlavors:          map[string]map[string]VariableFlavor{},
		targetOverrideSet:      map[string]map[string]bool{},
		targetPrivateVars:      map[string]map[string]bool{},
		patternVars:            map[string]map[string]string{},
		patternFlavors:         map[string]map[string]VariableFlavor{},
		patternOverrideSet:     map[string]map[string]bool{},
		patternPrivateVars:     map[string]map[string]bool{},
		privateGlobals:         map[string]bool{},
		envImported:            map[string]bool{},
		envOverride:            ctx.EnvironmentOverride,
		warnUndefined:          ctx.WarnUndefined,
		warnedUndefined:        map[string]bool{},
		makeProgram:            ctx.MakeProgram,
		makeFlags:              ctx.MakeFlags,
		goals:                  append([]string(nil), ctx.Goals...),
		exported:               map[string]bool{},
		unexported:             map[string]bool{},
		silentTargets:          map[string]bool{},
		ignoreTargets:          map[string]bool{},
		preciousTargets:        map[string]bool{},
		intermediateTargets:    map[string]bool{},
		notIntermediateTargets: map[string]bool{},
		secondaryTargets:       map[string]bool{},
		deleteOnErrorTargets:   map[string]bool{},
		recipePrefix:           '\t',
	}
	if !ctx.NoBuiltinVars {
		if err := state.seedBuiltins(resolved); err != nil {
			return nil, err
		}
	}
	if err := state.applyOverrides(ctx.Overrides); err != nil {
		return nil, err
	}
	if err := state.loadFile(resolved); err != nil {
		return nil, fmt.Errorf("parse %s: %w", resolved, err)
	}
	if !ctx.NoBuiltinRules {
		if err := state.seedDefaultRules(); err != nil {
			return nil, err
		}
	}
	if len(state.conditionals) != 0 {
		return nil, errors.New("unterminated conditional block")
	}
	state.prunePrivateGlobals()

	for _, target := range state.patternRules {
		state.project.PatternRules = append(state.project.PatternRules, *target)
	}
	visited := map[string]bool{}
	for _, name := range state.order {
		if visited[name] {
			continue
		}
		visited[name] = true

		first := state.targets[name]
		if first == nil {
			continue
		}

		if first.DoubleColon {
			for _, target := range state.allDoubleColon {
				if target.Name == name {
					state.applyTargetAttributes(target, name)
					state.project.Targets = append(state.project.Targets, *target)
				}
			}
		} else {
			state.applyTargetAttributes(first, name)
			state.project.Targets = append(state.project.Targets, *first)
		}
	}
	state.project.ExportAllVariables = state.exportAll
	state.project.ExportedVariables = copyBoolMap(state.exported)
	state.project.UnexportedVariables = copyBoolMap(state.unexported)
	state.project.VariableFlavors = make(map[string]VariableFlavor, len(state.varFlavors))
	for k, v := range state.varFlavors {
		state.project.VariableFlavors[k] = v
	}
	state.project.PatternVars = make(map[string]map[string]string, len(state.patternVars))
	for k, v := range state.patternVars {
		state.project.PatternVars[k] = copyStringMap(v)
	}
	state.project.PatternFlavors = make(map[string]map[string]VariableFlavor, len(state.patternFlavors))
	for k, v := range state.patternFlavors {
		state.project.PatternFlavors[k] = copyFlavorMap(v)
	}
	state.project.PatternOverrides = make(map[string]map[string]bool, len(state.patternOverrideSet))
	for k, v := range state.patternOverrideSet {
		state.project.PatternOverrides[k] = copyBoolMap(v)
	}
	state.project.PatternPrivateVars = make(map[string]map[string]bool, len(state.patternPrivateVars))
	for k, v := range state.patternPrivateVars {
		state.project.PatternPrivateVars[k] = copyBoolMap(v)
	}
	state.project.PatternOrder = append([]string(nil), state.patternOrder...)
	state.project.PrivateVariables = copyBoolMap(state.privateGlobals)

	// Translate suffix rules
	if len(state.suffixes) > 0 {
		var finalTargets []Target
		var suffixPatterns []Target
		for _, target := range state.project.Targets {
			if len(target.Deps) == 0 && len(target.OrderOnlyDeps) == 0 {
				if s1, s2, ok := isDoubleSuffixRule(target.Name, state.suffixes); ok {
					target.Name = "%" + s2
					target.Deps = []string{"%" + s1}
					suffixPatterns = append(suffixPatterns, target)
					continue
				}
				if s1, ok := isSingleSuffixRule(target.Name, state.suffixes); ok {
					target.Name = "%"
					target.Deps = []string{"%" + s1}
					suffixPatterns = append(suffixPatterns, target)
					continue
				}
			}
			finalTargets = append(finalTargets, target)
		}
		state.project.Targets = finalTargets
		// Suffix rules should override built-in rules but user pattern rules override suffix rules.
		// Since we can't easily interleave them, let's prepend them to PatternRules so they have high precedence.
		state.project.PatternRules = append(suffixPatterns, state.project.PatternRules...)
	}

	for i := range state.project.Targets {
		t := &state.project.Targets[i]
		if state.lowResolutionTimeAll || state.lowResolutionTimeTargets[t.Name] {
			t.LowResolutionTime = true
		}
	}

	if goal := firstDefaultTarget(state.order); goal != "" {
		state.project.DefaultTarget = goal
	} else if len(state.order) > 0 {
		state.project.DefaultTarget = state.order[0]
	}
	state.project.Variables = snapshotVariables(state.project.Variables, state.varFlavors)
	if goal := parseDefaultGoal(state.project.Variables[".DEFAULT_GOAL"]); goal != "" {
		state.project.DefaultTarget = goal
	}
	state.project.Shell = state.project.Variables["SHELL"]
	state.project.ShellFlags = state.project.Variables[".SHELLFLAGS"]
	state.project.SourcePath = resolved
	if err := state.project.normalize(); err != nil {
		return nil, err
	}
	return state.project, nil
}

func isDoubleSuffixRule(name string, suffixes []string) (string, string, bool) {
	for _, s1 := range suffixes {
		if strings.HasPrefix(name, s1) {
			s2 := name[len(s1):]
			for _, known := range suffixes {
				if s2 == known {
					return s1, s2, true
				}
			}
		}
	}
	return "", "", false
}

func isSingleSuffixRule(name string, suffixes []string) (string, bool) {
	for _, s1 := range suffixes {
		if name == s1 {
			return s1, true
		}
	}
	return "", false
}

func parseDefaultGoal(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (p *Project) Expand(input string, automatic map[string]string) string {
	vars := make(map[string]string, len(p.Variables)+len(automatic))
	for k, v := range p.Variables {
		vars[k] = v
	}
	flavors := make(map[string]VariableFlavor, len(p.VariableFlavors)+len(automatic))
	for k, v := range p.VariableFlavors {
		flavors[k] = v
	}
	for k, v := range automatic {
		vars[k] = v
		flavors[k] = FlavorSimple
	}
	// Use ExpandVarsWithOptions directly to set preserveAutomatic to false
	return ExpandVarsWithOptions(input, vars, flavors, nil, true, false, nil, nil)
}

func (p *Project) ExpandPrerequisites(deps []string, automatic map[string]string) []string {
	var expanded []string
	for _, dep := range deps {
		res := p.Expand(dep, automatic)
		expanded = append(expanded, splitFields(res)...)
	}
	return expanded
}

func firstDefaultTarget(order []string) string {
	for _, name := range order {
		if strings.HasPrefix(name, ".") {
			continue
		}
		return name
	}
	return ""
}

func resolveInput(path string) (string, Format, error) {
	if path == "" || path == "." {
		return detectProjectFile(".")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return detectProjectFile(path)
	}

	format := detectFormat(path)
	if format != FormatMakefile {
		return "", "", fmt.Errorf("unsupported project file %q: only Makefile and makefile are supported", filepath.Base(path))
	}
	return path, format, nil
}

func detectProjectFile(dir string) (string, Format, error) {
	for _, name := range []string{"Makefile", "makefile"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, FormatMakefile, nil
		}
	}
	return "", "", fmt.Errorf("no supported project file found in %s", dir)
}

func detectFormat(path string) Format {
	base := filepath.Base(path)
	if base == "Makefile" || base == "makefile" {
		return FormatMakefile
	}
	return ""
}

func (s *parserState) seedBuiltins(sourcePath string) error {
	dir, err := filepath.Abs(filepath.Dir(sourcePath))
	if err != nil {
		return fmt.Errorf("resolve project directory: %w", err)
	}
	s.project.Variables["CURDIR"] = dir
	s.varFlavors["CURDIR"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "CURDIR", "default")
	s.project.Variables["MAKE"] = makeProgramName(s.makeProgram)
	s.varFlavors["MAKE"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MAKE", "default")
	s.project.Variables["MAKECMDGOALS"] = strings.Join(s.goals, " ")
	s.varFlavors["MAKECMDGOALS"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MAKECMDGOALS", "default")
	s.project.Variables["MAKEFILE_LIST"] = ""
	s.varFlavors["MAKEFILE_LIST"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MAKEFILE_LIST", "default")
	s.project.Variables["MAKEFLAGS"] = strings.TrimSpace(s.makeFlags)
	s.varFlavors["MAKEFLAGS"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MAKEFLAGS", "default")
	s.project.Variables["MFLAGS"] = strings.TrimSpace(s.makeFlags)
	s.varFlavors["MFLAGS"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MFLAGS", "default")
	s.project.Variables["MAKELEVEL"] = nextMakeLevel(os.Getenv("MAKELEVEL"))
	s.varFlavors["MAKELEVEL"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "MAKELEVEL", "default")
	s.project.Variables[".SHELLFLAGS"] = "-c"
	s.varFlavors[".SHELLFLAGS"] = FlavorSimple
	setVariableOrigin(s.project.Variables, ".SHELLFLAGS", "default")

	s.project.Variables["CC"] = "cc"
	s.varFlavors["CC"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "CC", "default")
	s.project.Variables["CXX"] = "g++"
	s.varFlavors["CXX"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "CXX", "default")
	s.project.Variables["AR"] = "ar"
	s.varFlavors["AR"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "AR", "default")
	s.project.Variables["RM"] = "rm -f"
	s.varFlavors["RM"] = FlavorSimple
	setVariableOrigin(s.project.Variables, "RM", "default")

	s.importEnvironment()
	return nil
}

func (s *parserState) seedDefaultRules() error {
	defaultRules := []string{
		"%.o: %.c ; $(CC) $(CPPFLAGS) $(CFLAGS) -c -o $@ $<",
		"%.o: %.cc ; $(CXX) $(CPPFLAGS) $(CXXFLAGS) -c -o $@ $<",
		"%.o: %.cpp ; $(CXX) $(CPPFLAGS) $(CXXFLAGS) -c -o $@ $<",
	}
	for _, rule := range defaultRules {
		if err := s.parseRawLine("built-in", rule); err != nil {
			return err
		}
	}
	return nil
}

func nextMakeLevel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "0"
	}
	level, err := strconv.Atoi(raw)
	if err != nil || level < 0 {
		return "0"
	}
	return strconv.Itoa(level + 1)
}

func (s *parserState) importEnvironment() {
	for _, entry := range os.Environ() {
		idx := strings.Index(entry, "=")
		if idx <= 0 {
			continue
		}
		key := entry[:idx]
		if _, exists := s.project.Variables[key]; exists {
			continue
		}
		s.project.Variables[key] = entry[idx+1:]
		s.varFlavors[key] = FlavorRecursive
		s.envImported[key] = true
		setVariableOrigin(s.project.Variables, key, "environment")
	}
}

func makeProgramName(program string) string {
	name := filepath.Base(strings.TrimSpace(program))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "gomake"
	}
	return name
}

func (s *parserState) applyOverrides(overrides map[string]string) error {
	for key, value := range overrides {
		if err := applyAssignment(s.project.Variables, s.varFlavors, key, value, "="); err != nil {
			return err
		}
		s.overrides[key] = true
		setVariableOrigin(s.project.Variables, key, "command line")
	}
	return nil
}

func (s *parserState) applyTargetAttributes(target *Target, name string) {
	target.Phony = s.phony[name]
	target.Silent = s.silentAll || s.silentTargets[name]
	target.IgnoreErrors = s.ignoreAll || s.ignoreTargets[name]
	target.Precious = s.preciousAll || s.preciousTargets[name]
	target.Intermediate = s.intermediateTargets[name] || s.secondaryAll || s.secondaryTargets[name]
	if s.notIntermediateAll || s.notIntermediateTargets[name] {
		target.Intermediate = false
	}
	target.Secondary = s.secondaryAll || s.secondaryTargets[name]
	target.DeleteOnError = s.deleteOnErrorAll || s.deleteOnErrorTargets[name]
	target.Variables = copyStringMap(s.targetVars[name])
	target.VariableFlavors = copyFlavorMap(s.targetFlavors[name])
	target.OverrideSet = copyBoolMap(s.targetOverrideSet[name])
	target.PrivateVariables = copyBoolMap(s.targetPrivateVars[name])
}

func copyBoolMap(input map[string]bool) map[string]bool {
	if input == nil {
		return nil
	}
	out := make(map[string]bool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func copyStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func copyFlavorMap(input map[string]VariableFlavor) map[string]VariableFlavor {
	if input == nil {
		return nil
	}
	out := make(map[string]VariableFlavor, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
