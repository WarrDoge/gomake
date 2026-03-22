package config

import (
	"errors"
	"fmt"
)

type Format string

const (
	FormatMakefile Format = "makefile"
)

type Project struct {
	DefaultTarget       string
	Variables           map[string]string
	VariableFlavors     map[string]VariableFlavor
	Targets             []Target
	PatternRules        []Target
	VPaths              []VPath
	Format              Format
	PatternVars         map[string]map[string]string
	PatternFlavors      map[string]map[string]VariableFlavor
	PatternOverrides    map[string]map[string]bool
	PatternPrivateVars  map[string]map[string]bool
	PatternOrder        []string
	PrivateVariables    map[string]bool
	SourcePath          string
	OneShell            bool
	SecondExpansion     bool
	NotParallel         bool
	Shell               string
	ShellFlags          string
	ExportAllVariables  bool
	ExportedVariables   map[string]bool
	UnexportedVariables map[string]bool
}

type Target struct {
	Name              string
	Deps              []string
	OrderOnlyDeps     []string
	Commands          []RecipeCommand
	Phony             bool
	Silent            bool
	IgnoreErrors      bool
	DeleteOnError     bool
	Precious          bool
	Intermediate      bool
	Secondary         bool
	LowResolutionTime bool
	DoubleColon       bool
	Explicit          bool
	GroupTargets      []string
	Variables         map[string]string
	VariableFlavors   map[string]VariableFlavor
	OverrideSet       map[string]bool
	PrivateVariables  map[string]bool
}

type VPath struct {
	Pattern     string
	Directories []string
}

type RecipeCommand struct {
	Text        string
	Silent      bool
	IgnoreError bool
	Force       bool
	Recursive   bool
}

type parserState struct {
	project                  *Project
	phony                    map[string]bool
	targets                  map[string]*Target
	varFlavors               map[string]VariableFlavor
	overrides                map[string]bool
	overrideSet              map[string]bool
	targetVars               map[string]map[string]string
	targetFlavors            map[string]map[string]VariableFlavor
	targetOverrideSet        map[string]map[string]bool
	targetPrivateVars        map[string]map[string]bool
	patternVars              map[string]map[string]string
	patternFlavors           map[string]map[string]VariableFlavor
	patternOverrideSet       map[string]map[string]bool
	patternPrivateVars       map[string]map[string]bool
	patternOrder             []string
	recursiveExpansionErr    error
	privateGlobals           map[string]bool
	envImported              map[string]bool
	envOverride              bool
	posix                    bool
	warnUndefined            bool
	warnedUndefined          map[string]bool
	makeProgram              string
	makeFlags                string
	goals                    []string
	silentAll                bool
	silentTargets            map[string]bool
	ignoreAll                bool
	ignoreTargets            map[string]bool
	preciousAll              bool
	preciousTargets          map[string]bool
	intermediateTargets      map[string]bool
	notIntermediateTargets   map[string]bool
	notIntermediateAll       bool
	secondaryTargets         map[string]bool
	secondaryAll             bool
	lowResolutionTimeTargets map[string]bool
	lowResolutionTimeAll     bool
	deleteOnErrorAll         bool
	deleteOnErrorTargets     map[string]bool

	exportAll      bool
	exported       map[string]bool
	unexported     map[string]bool
	recipePrefix   byte
	define         *defineBlock
	order          []string
	current        []*Target
	patternRules   []*Target
	allDoubleColon []*Target
	suffixes       []string
	conditionals   []conditionalFrame
}

type defineBlock struct {
	name     string
	operator string
	lines    []string
}

type conditionalFrame struct {
	parentActive bool
	conditionMet bool
	active       bool
	inElse       bool
}

type VariableFlavor uint8

const (
	FlavorRecursive VariableFlavor = iota
	FlavorSimple
)

func (p *Project) normalize() error {
	if p.Variables == nil {
		p.Variables = map[string]string{}
	}
	if len(p.Targets) == 0 {
		return errors.New("project must contain at least one target")
	}

	seen := make(map[string]struct{}, len(p.Targets))
	for idx := range p.Targets {
		target := &p.Targets[idx]
		if target.Name == "" {
			return fmt.Errorf("target %d has no name", idx)
		}
		if _, ok := seen[target.Name]; ok && !target.DoubleColon {
			return fmt.Errorf("duplicate target %q", target.Name)
		}
		seen[target.Name] = struct{}{}
	}

	if p.DefaultTarget == "" {
		p.DefaultTarget = p.Targets[0].Name
	}
	if _, ok := seen[p.DefaultTarget]; !ok {
		return fmt.Errorf("default target %q does not exist", p.DefaultTarget)
	}
	return nil
}
