package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gomake/internal/config"
)

func TestResolveBuildOrder(t *testing.T) {
	project := &config.Project{
		DefaultTarget: "app",
		Targets: []config.Target{
			{Name: "prepare"},
			{Name: "lib.a", Deps: []string{"prepare"}},
			{Name: "app", Deps: []string{"lib.a"}},
		},
	}

	engine, err := New(project, Options{RootDir: "."})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	order, err := engine.resolve("app")
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}

	want := []string{"prepare", "lib.a", "app"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("resolve() = %v, want %v", order, want)
	}
}

func TestBuildRunsIndependentTargetsInParallel(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Deps: []string{"first", "second"}},
			{Name: "first", Phony: true, Commands: []config.RecipeCommand{{Text: "sleep 2; printf 'first' > first.txt"}}},
			{Name: "second", Phony: true, Commands: []config.RecipeCommand{{Text: "sleep 2; printf 'second' > second.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Jobs: 2})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	started := time.Now()
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	elapsed := time.Since(started)
	if elapsed >= 3500*time.Millisecond {
		t.Fatalf("elapsed = %v, want < 3.5s for parallel execution", elapsed)
	}

	if got := readFileString(t, filepath.Join(dir, "first.txt")); got != "first" {
		t.Fatalf("first.txt = %q, want first", got)
	}
	if got := readFileString(t, filepath.Join(dir, "second.txt")); got != "second" {
		t.Fatalf("second.txt = %q, want second", got)
	}
}

func TestBuildNotParallelForcesSequentialExecution(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		NotParallel:   true,
		Targets: []config.Target{
			{Name: "all", Phony: true, Deps: []string{"first", "second"}},
			{Name: "first", Phony: true, Commands: []config.RecipeCommand{{Text: "sleep 2; printf 'first' > first.txt"}}},
			{Name: "second", Phony: true, Commands: []config.RecipeCommand{{Text: "sleep 2; printf 'second' > second.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Jobs: 2})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	started := time.Now()
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	elapsed := time.Since(started)
	if elapsed < 3500*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 3.5s when .NOTPARALLEL is set", elapsed)
	}
}

func TestBuildPhonyTarget(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > result.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "ok" {
		t.Fatalf("result = %q, want ok", string(content))
	}
}

func TestBuildSkipsUpToDateFileTarget(t *testing.T) {
	dir := t.TempDir()
	depPath := filepath.Join(dir, "input.txt")
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(depPath, []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(dep) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("target content = %q, want existing", string(content))
	}
}

func TestBuildRebuildsMissingFileTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'built' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "built" {
		t.Fatalf("target content = %q, want built", string(content))
	}
}

func TestBuildRebuildsWhenPrerequisiteIsNewer(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(dep) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "rebuilt" {
		t.Fatalf("target content = %q, want rebuilt", string(content))
	}
}

func TestBuildFindsPrerequisitesViaVPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'built' > output.txt"}}},
		},
		VPaths: []config.VPath{{Pattern: "%.txt", Directories: []string{"src"}}},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "built" {
		t.Fatalf("target content = %q, want built", string(content))
	}
}

func TestBuildAutomaticVarsUseVPathResolvedPrerequisites(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range []string{"input.txt", "extra.txt"} {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt", "input.txt", "extra.txt"}, Commands: []config.RecipeCommand{{Text: "printf '%s|%s|%s' '$<' '$^' '$+' > output.txt"}}},
		},
		VPaths: []config.VPath{{Pattern: "%.txt", Directories: []string{"src"}}},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(content), "src/input.txt|src/input.txt src/extra.txt|src/input.txt src/input.txt src/extra.txt"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestBuildSkipsUpToDateFileTargetWithVPathPrerequisite(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	depPath := filepath.Join(dir, "src", "input.txt")
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(depPath, []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(dep) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
		VPaths: []config.VPath{{Pattern: "%.txt", Directories: []string{"src"}}},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("target content = %q, want existing", string(content))
	}
}

func TestBuildFindsPrerequisitesViaVPATHVariable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "out.txt",
		Variables:     map[string]string{"VPATH": "src"},
		Targets: []config.Target{
			{Name: "out.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf '%s' '$<' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(content), "src/input.txt"; got != want {
		t.Fatalf("out = %q, want %q", got, want)
	}
}

func TestBuildTreatsPhonyPrerequisiteAsOutOfDate(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "prepare", Phony: true},
			{Name: "output.txt", Deps: []string{"prepare"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "rebuilt" {
		t.Fatalf("target content = %q, want rebuilt", string(content))
	}
}

// Under .ONESHELL, GNU make honors the recipe prefix (-, @, +) only on the
// first line; an ignore prefix on a later line does not suppress its failure.
func TestBuildOneShellIgnorePrefixOnLaterLineDoesNotSuppressFailure(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		OneShell:      true,
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "printf 'ok' > result.txt"},
					{Text: "false", IgnoreError: true},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = engine.Build("all")
	if err == nil || !strings.Contains(err.Error(), "shell command failed") {
		t.Fatalf("Build() error = %v, want shell command failure", err)
	}
	// The recipe still ran up to the failing command.
	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "ok" {
		t.Fatalf("result = %q, want ok", string(content))
	}
}

// An ignore prefix on the first line suppresses failures for the whole
// .ONESHELL recipe, including a failing final command.
func TestBuildOneShellIgnorePrefixOnFirstLineSuppressesWholeRecipe(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		OneShell:      true,
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "false", IgnoreError: true},
					{Text: "false"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v, want success with first-line ignore prefix", err)
	}
}

func TestResolveCycle(t *testing.T) {
	project := &config.Project{
		DefaultTarget: "app",
		Targets: []config.Target{
			{Name: "app", Deps: []string{"lib"}},
			{Name: "lib", Deps: []string{"app"}},
		},
	}

	engine, err := New(project, Options{RootDir: "."})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = engine.resolve("app")
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("resolve() error = %v, want dependency cycle", err)
	}
}

func TestBuildFailsOnMissingPrerequisite(t *testing.T) {
	project := &config.Project{
		DefaultTarget: "app",
		Targets: []config.Target{
			{Name: "app", Deps: []string{"missing.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'ok' > app"}}},
		},
	}

	engine, err := New(project, Options{RootDir: "."})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = engine.Build("app")
	if err == nil || !strings.Contains(err.Error(), `missing prerequisite "missing.txt"`) {
		t.Fatalf("Build() error = %v, want missing prerequisite", err)
	}
}

func TestBuildUsesDefaultRuleForMissingPrerequisite(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: ".DEFAULT", Commands: []config.RecipeCommand{{Text: "printf 'dep' > $@"}}},
			{Name: "out.txt", Deps: []string{"dep.txt"}, Commands: []config.RecipeCommand{{Text: "cat $< > $@"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := readFileString(t, filepath.Join(dir, "dep.txt")); got != "dep" {
		t.Fatalf("dep.txt = %q, want dep", got)
	}
	if got := readFileString(t, filepath.Join(dir, "out.txt")); got != "dep" {
		t.Fatalf("out.txt = %q, want dep", got)
	}
}

func TestBuildUnknownTarget(t *testing.T) {
	project := &config.Project{
		DefaultTarget: "app",
		Targets: []config.Target{
			{Name: "app"},
		},
	}

	engine, err := New(project, Options{RootDir: "."})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = engine.Build("missing")
	if err == nil || !strings.Contains(err.Error(), `unknown target "missing"`) {
		t.Fatalf("Build() error = %v, want unknown target", err)
	}
}

func TestBuildUnknownExistingFileTargetSucceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("existing.txt"); err != nil {
		t.Fatalf("Build() error = %v, want nil for existing file target", err)
	}
}

func TestBuildUnknownTargetUsesDefaultRule(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: ".DEFAULT", Commands: []config.RecipeCommand{{Text: "printf 'made:%s' '$@' > $@"}}},
			{Name: "all", Phony: true},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("missing.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := readFileString(t, filepath.Join(dir, "missing.txt")); got != "made:missing.txt" {
		t.Fatalf("missing.txt = %q, want made:missing.txt", got)
	}
}

func TestBuildSkipsUpToDateTargetWithTargetPrerequisite(t *testing.T) {
	dir := t.TempDir()
	depPath := filepath.Join(dir, "dep.txt")
	outPath := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(depPath, []byte("dep"), 0o644); err != nil {
		t.Fatalf("WriteFile(dep) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(outPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "dep.txt"},
			{Name: "out.txt", Deps: []string{"dep.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := readFileString(t, outPath); got != "existing" {
		t.Fatalf("out.txt = %q, want existing", got)
	}
}

func TestBuildDeleteOnErrorRemovesFailedTarget(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", DeleteOnError: true, Commands: []config.RecipeCommand{{Text: "printf 'partial' > out.txt; false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err == nil {
		t.Fatal("Build() error = nil, want failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(out.txt) error = %v, want not exists", err)
	}
}

func TestBuildDeleteOnErrorDisabledKeepsFailedTarget(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", Commands: []config.RecipeCommand{{Text: "printf 'partial' > out.txt; false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err == nil {
		t.Fatal("Build() error = nil, want failure")
	}
	if got := readFileString(t, filepath.Join(dir, "out.txt")); got != "partial" {
		t.Fatalf("out.txt = %q, want partial", got)
	}
}

func TestBuildDeleteOnErrorKeepsPreciousTarget(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", DeleteOnError: true, Precious: true, Commands: []config.RecipeCommand{{Text: "printf 'partial' > out.txt; false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err == nil {
		t.Fatal("Build() error = nil, want failure")
	}
	if got := readFileString(t, filepath.Join(dir, "out.txt")); got != "partial" {
		t.Fatalf("out.txt = %q, want partial for precious target", got)
	}
}

func TestRunTargetExpandsAutomaticVars(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt", "input.txt", "extra.txt"}, Commands: []config.RecipeCommand{{Text: "printf '%s|%s|%s' '$@' '$<' '$^' > output.txt"}}},
		},
	}

	for _, name := range []string{"input.txt", "extra.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "output.txt|input.txt|input.txt extra.txt" {
		t.Fatalf("output = %q, want automatic variable expansion", string(content))
	}
}

func TestRunTargetPreservesEscapedDollars(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Commands: []config.RecipeCommand{{Text: "value=$$HOME; printf '%s' \"$${value}\" > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) == "" {
		t.Fatal("output = empty, want expanded shell variable content")
	}
}

func TestBuildRecipeSingleCharacterVariableReferencesMatchMakeSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	content := "X = make\nall:\n\tX=shell; printf '%s|%s' '$X' \"$${X}\" > out.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	project, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	contentOut, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile(out.txt) error = %v", err)
	}
	if got, want := string(contentOut), "make|shell"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestRunRecipeTargetPropagatesFailure(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = engine.Build("all")
	if err == nil || !strings.Contains(err.Error(), "shell command failed") {
		t.Fatalf("Build() error = %v, want failure", err)
	}
}

func TestRunRecipeTargetIgnoresFailureWithDashPrefix(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "false", IgnoreError: true},
					{Text: "printf 'after' > out.txt"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "after" {
		t.Fatalf("output = %q, want after", string(content))
	}
}

func TestRunRecipeTargetIgnoresFailureWithIgnoreTarget(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{
				Name:         "all",
				Phony:        true,
				IgnoreErrors: true,
				Commands: []config.RecipeCommand{
					{Text: "false"},
					{Text: "printf 'after' > out.txt"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "after" {
		t.Fatalf("output = %q, want after", string(content))
	}
}

func TestRunTargetSilentSuppressesCommandEcho(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{
				Name:   "all",
				Phony:  true,
				Silent: true,
				Commands: []config.RecipeCommand{
					{Text: "printf 'ok' > out.txt"},
				},
			},
		},
	}

	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
	})

	engine, err := New(project, Options{RootDir: dir, Verbose: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Fatalf("stdout = %q, want no command echo", string(output))
	}
}

func TestRunTargetExportsMarkedVariables(t *testing.T) {
	dir := t.TempDir()
	key := "GOMAKE_EXPORT_MARKED"
	// $$ so make passes $VAR through to the shell, which reads it from the exported environment.
	commandText := fmt.Sprintf("printf '%%s' \"$$%s\" > out.txt", key)
	project := &config.Project{
		DefaultTarget: "all",
		Variables: map[string]string{
			key: "from-file",
		},
		ExportedVariables: map[string]bool{key: true},
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: commandText}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "from-file" {
		t.Fatalf("exported value = %q, want from-file", got)
	}
}

func TestRunTargetUnexportRemovesEnvironmentValue(t *testing.T) {
	dir := t.TempDir()
	key := "GOMAKE_EXPORT_UNEXPORT"
	t.Setenv(key, "from-env")
	// $$ so make passes $VAR through to the shell, which reads it from the exported environment.
	commandText := fmt.Sprintf("printf '%%s' \"$$%s\" > out.txt", key)
	project := &config.Project{
		DefaultTarget: "all",
		Variables: map[string]string{
			key: "from-file",
		},
		UnexportedVariables: map[string]bool{key: true},
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: commandText}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "" {
		t.Fatalf("unexported value = %q, want empty", got)
	}
}

func TestRunTargetExportAllVariables(t *testing.T) {
	dir := t.TempDir()
	key := "GOMAKE_EXPORT_ALL"
	// $$ so make passes $VAR through to the shell, which reads it from the exported environment.
	commandText := fmt.Sprintf("printf '%%s' \"$$%s\" > out.txt", key)
	project := &config.Project{
		DefaultTarget:      "all",
		ExportAllVariables: true,
		Variables: map[string]string{
			key: "from-file",
		},
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: commandText}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "from-file" {
		t.Fatalf("export-all value = %q, want from-file", got)
	}
}

func TestRunTargetUsesConfiguredShell(t *testing.T) {
	shellPath := "/bin/sh"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skipf("shell %s unavailable: %v", shellPath, err)
	}

	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Shell:         shellPath,
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > result.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "ok" {
		t.Fatalf("result = %q, want ok", string(content))
	}
}

func TestRunTargetFailsWhenConfiguredShellCannotStart(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Shell:         filepath.Join(dir, "missing-shell"),
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > result.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = engine.Build("all")
	if err == nil || !strings.Contains(err.Error(), "start shell") {
		t.Fatalf("Build() error = %v, want shell startup failure", err)
	}
}

func TestRunTargetOneShell(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		OneShell:      true,
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "foo=bar"},
					{Text: "printf '%s' \"$$foo\" > out.txt"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "bar" {
		t.Fatalf("output = %q, want bar", string(content))
	}
}

func TestBuildIgnoresOrderOnlyTimestampForFreshness(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "stamp"), []byte("stamp"), 0o644); err != nil {
		t.Fatalf("WriteFile(stamp) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{
				Name:          "output.txt",
				Deps:          []string{"input.txt"},
				OrderOnlyDeps: []string{"stamp"},
				Commands:      []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("output = %q, want existing", string(content))
	}
}

func TestRunTargetExpandsOrderOnlyAutomaticVar(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{
				Name:          "output.txt",
				OrderOnlyDeps: []string{"stamp"},
				Commands:      []config.RecipeCommand{{Text: "printf '%s' '$|' > output.txt"}},
			},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, "stamp"), []byte("stamp"), 0o644); err != nil {
		t.Fatalf("WriteFile(stamp) error = %v", err)
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "stamp" {
		t.Fatalf("output = %q, want stamp", string(content))
	}
}

func TestRunTargetExpandsAdditionalAutomaticVars(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile(new) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "target.txt",
		Targets: []config.Target{
			{
				Name: "target.txt",
				Deps: []string{"old.txt", "new.txt", "new.txt"},
				Commands: []config.RecipeCommand{
					{Text: "printf '%s|%s|%s|%s' '$+' '$?' '$(+)' '$(?)' > target.txt"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("target.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "target.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(content), "old.txt new.txt new.txt|new.txt new.txt|old.txt new.txt new.txt|new.txt new.txt"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunTargetExpandsAutomaticVarDirFileVariants(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"src/input.txt", "assets/extra.txt"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	project := &config.Project{
		DefaultTarget: "build/out.txt",
		Targets: []config.Target{
			{
				Name: "build/out.txt",
				Deps: []string{"src/input.txt", "assets/extra.txt"},
				Commands: []config.RecipeCommand{
					{Text: "mkdir -p build && printf '%s|%s|%s|%s|%s|%s|%s' '$(@D)' '$(@F)' '$(<D)' '$(<F)' '$(^D)' '$(^F)' '$(*F)' > build/out.txt"},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("build/out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "build", "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(content), "build|out.txt|src|input.txt|src assets|input.txt extra.txt|out"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunTargetUsesConfiguredShellFlags(t *testing.T) {
	shellPath := "/bin/sh"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skipf("shell %s unavailable: %v", shellPath, err)
	}

	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Shell:         shellPath,
		ShellFlags:    "-ec",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "false; printf 'ok' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = engine.Build("all")
	if err == nil || !strings.Contains(err.Error(), "shell command failed") {
		t.Fatalf("Build() error = %v, want shell command failure", err)
	}
}

func TestBuildDryRunSkipsNonForcedCommands(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(out.txt) error = %v, want not exists", err)
	}
}

func TestBuildDryRunExecutesForcedCommands(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > out.txt", Force: true}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile(out.txt) error = %v", err)
	}
	if got := string(content); got != "ok" {
		t.Fatalf("content = %q, want ok", got)
	}
}

func TestBuildDryRunExecutesRecursiveCommands(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'ok' > out.txt", Recursive: true}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile(out.txt) error = %v", err)
	}
	if got := string(content); got != "ok" {
		t.Fatalf("content = %q, want ok", got)
	}
}

func TestBuildDryRunOneShellRunsOnlyForcedCommands(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		OneShell:      true,
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "printf 'normal' > normal.txt"},
					{Text: "printf 'forced' > forced.txt", Force: true},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "normal.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(normal.txt) error = %v, want not exists", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "forced.txt"))
	if err != nil {
		t.Fatalf("ReadFile(forced.txt) error = %v", err)
	}
	if got := string(content); got != "forced" {
		t.Fatalf("content = %q, want forced", got)
	}
}

func TestBuildDryRunOneShellRunsOnlyRecursiveCommands(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		OneShell:      true,
		Targets: []config.Target{
			{
				Name:  "all",
				Phony: true,
				Commands: []config.RecipeCommand{
					{Text: "printf 'normal' > normal.txt"},
					{Text: "printf 'recursive' > recursive.txt", Recursive: true},
				},
			},
		},
	}

	engine, err := New(project, Options{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("all"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "normal.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(normal.txt) error = %v, want not exists", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "recursive.txt"))
	if err != nil {
		t.Fatalf("ReadFile(recursive.txt) error = %v", err)
	}
	if got := string(content); got != "recursive" {
		t.Fatalf("content = %q, want recursive", got)
	}
}

func TestBuildKeepGoingContinuesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	project := &config.Project{
		DefaultTarget: "all",
		Targets: []config.Target{
			{Name: "all", Phony: true, Deps: []string{"b", "c"}},
			{Name: "a", Phony: true, Commands: []config.RecipeCommand{{Text: "false"}}},
			{Name: "b", Deps: []string{"a"}, Commands: []config.RecipeCommand{{Text: "printf 'b' > b.txt"}}},
			{Name: "c", Phony: true, Commands: []config.RecipeCommand{{Text: "printf 'c' > c.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, KeepGoing: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = engine.Build("all")
	if err == nil {
		t.Fatal("Build() error = nil, want keep-going error")
	}

	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(b.txt) error = %v, want not exists", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "c.txt"))
	if err != nil {
		t.Fatalf("ReadFile(c.txt) error = %v", err)
	}
	if got := string(content); got != "c" {
		t.Fatalf("content = %q, want c", got)
	}
}

func TestBuildTouchCreatesMissingTargetWithoutRunningCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Touch: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Fatalf("Stat(out.txt) error = %v", err)
	}
}

func TestBuildTouchUpdatesTimestamp(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(targetPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}
	initialInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Stat(out) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "false"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Touch: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	updatedInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Stat(out) error = %v", err)
	}
	if !updatedInfo.ModTime().After(initialInfo.ModTime()) {
		t.Fatalf("modtime = %v, want after %v", updatedInfo.ModTime(), initialInfo.ModTime())
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(out) error = %v", err)
	}
	if got := string(content); got != "keep" {
		t.Fatalf("content = %q, want keep", got)
	}
}

func TestBuildQuestionModeReturnsOutOfDateError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'ok' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Question: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = engine.Build("out.txt")
	if !errors.Is(err, ErrOutOfDate) {
		t.Fatalf("Build() error = %v, want ErrOutOfDate", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("Stat(out.txt) error = %v, want not exists", err)
	}
}

func TestBuildQuestionModeSucceedsWhenUpToDate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(input) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("out"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}
	project := &config.Project{
		DefaultTarget: "out.txt",
		Targets: []config.Target{
			{Name: "out.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'ok' > out.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, Question: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("out.txt"); err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
}

func TestBuildRebuildsWhenWhatIfMarksPrerequisite(t *testing.T) {
	dir := t.TempDir()
	depPath := filepath.Join(dir, "input.txt")
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(depPath, []byte("input"), 0o644); err != nil {
		t.Fatalf("WriteFile(dep) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Deps: []string{"input.txt"}, Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, WhatIf: []string{"input.txt"}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(target) error = %v", err)
	}
	if got := string(content); got != "rebuilt" {
		t.Fatalf("content = %q, want rebuilt", got)
	}
}

func TestBuildRebuildsWhenWhatIfMarksTarget(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(targetPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	project := &config.Project{
		DefaultTarget: "output.txt",
		Targets: []config.Target{
			{Name: "output.txt", Commands: []config.RecipeCommand{{Text: "printf 'rebuilt' > output.txt"}}},
		},
	}

	engine, err := New(project, Options{RootDir: dir, WhatIf: []string{"output.txt"}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Build("output.txt"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(target) error = %v", err)
	}
	if got := string(content); got != "rebuilt" {
		t.Fatalf("content = %q, want rebuilt", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}
