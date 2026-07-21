package build

import (
	"os"
	"path/filepath"
	"testing"

	"gomake/internal/config"
)

// recipeOutput loads makefile into a temp dir, builds target, and returns the
// contents of outFile. Recipe variable expansion is deferred to build time
// (GNU semantics), so these resolution behaviors are verified end-to-end here
// rather than against stored recipe text at parse time.
func recipeOutput(t *testing.T, makefile string, overrides map[string]string, target, outFile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	project, err := config.LoadWithContext(dir, config.LoadContext{
		Overrides: overrides,
		Goals:     []string{target},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	engine, err := New(project, Options{RootDir: dir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer engine.Close()
	if err := engine.Build(target); err != nil {
		t.Fatalf("Build(%q) error = %v", target, err)
	}
	return readFileString(t, filepath.Join(dir, outFile))
}

func TestRecipeVariableResolution(t *testing.T) {
	cases := []struct {
		name      string
		makefile  string
		overrides map[string]string
		target    string
		out       string
		want      string
	}{
		{"target-specific value", "MODE = global\napp: MODE = local\napp:\n\tprintf '%s' '$(MODE)' > app.txt\n", nil, "app", "app.txt", "local"},
		{"target-specific leaves siblings global", "MODE = global\napp: MODE = local\nall:\n\tprintf '%s' '$(MODE)' > all.txt\n", nil, "all", "all.txt", "global"},
		{"target append inherits global", "CFLAGS = -O2\napp: CFLAGS += -g\napp:\n\tprintf '%s' '$(CFLAGS)' > app.txt\n", nil, "app", "app.txt", "-O2 -g"},
		{"target ?= respects existing", "MODE = global\napp: MODE ?= local\napp:\n\tprintf '%s' '$(MODE)' > app.txt\n", nil, "app", "app.txt", "global"},
		{"command line beats target-specific", "MODE = file\napp: MODE = local\napp:\n\tprintf '%s' '$(MODE)' > app.txt\n", map[string]string{"MODE": "cli"}, "app", "app.txt", "cli"},
		{"target override beats command line", "app: override MODE = local\napp: MODE = later\napp:\n\tprintf '%s' '$(MODE)' > app.txt\n", map[string]string{"MODE": "cli"}, "app", "app.txt", "local"},
		{"target override append beats command line", "app: override CFLAGS += -g\napp:\n\tprintf '%s' '$(CFLAGS)' > app.txt\n", map[string]string{"CFLAGS": "-O2"}, "app", "app.txt", "-O2 -g"},
		{"target override origin", "app: override MODE = local\napp:\n\tprintf '%s|%s' '$(MODE)' '$(origin MODE)' > app.txt\n", map[string]string{"MODE": "cli"}, "app", "app.txt", "local|override"},
		{"target non-override keeps command line origin", "app: MODE = local\napp:\n\tprintf '%s|%s' '$(MODE)' '$(origin MODE)' > app.txt\n", map[string]string{"MODE": "cli"}, "app", "app.txt", "cli|command line"},
		{"pattern-specific matching", "MODE = global\n%.txt: MODE = pattern\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\n", nil, "one.txt", "one.txt", "pattern"},
		{"pattern-specific non-matching global", "MODE = global\n%.txt: MODE = pattern\none.bin:\n\tprintf '%s' '$(MODE)' > one.bin\n", nil, "one.bin", "one.bin", "global"},
		{"pattern command line precedence", "%.txt: MODE = pattern\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\n", map[string]string{"MODE": "cli"}, "one.txt", "one.txt", "cli"},
		{"pattern override beats command line", "%.txt: override MODE = pattern\n%.txt: MODE = later\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\n", map[string]string{"MODE": "cli"}, "one.txt", "one.txt", "pattern"},
		{"pattern override origin", "%.txt: override MODE = pattern\none.txt:\n\tprintf '%s|%s' '$(MODE)' '$(origin MODE)' > one.txt\n", map[string]string{"MODE": "cli"}, "one.txt", "one.txt", "pattern|override"},
		{"target non-override blocks pattern override under command line", "%.txt: override MODE = pattern\none.txt: MODE = target\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\n", map[string]string{"MODE": "cli"}, "one.txt", "one.txt", "cli"},
		{"target beats pattern", "MODE = global\n%.txt: MODE = pattern\none.txt: MODE = target\none.txt:\n\tprintf '%s' '$(MODE)' > one.txt\n", nil, "one.txt", "one.txt", "target"},
		{"pattern append inherits global", "CFLAGS = -O2\n%.o: CFLAGS += -g\nmain.o:\n\tprintf '%s' '$(CFLAGS)' > main.flags\n", nil, "main.o", "main.flags", "-O2 -g"},
		{"pattern append command line quirk", "%.txt: CFLAGS += -g\none.txt:\n\tprintf '%s|%s' '$(value CFLAGS)' '$(CFLAGS)' > out.txt\n", map[string]string{"CFLAGS": "-O2"}, "one.txt", "out.txt", "-O2|-O2 -O2"},
		{"pattern multiple appends command line quirk", "%.txt: CFLAGS += A\n%.txt: CFLAGS += B\none.txt:\n\tprintf '%s|%s' '$(value CFLAGS)' '$(CFLAGS)' > out.txt\n", map[string]string{"CFLAGS": "cli"}, "one.txt", "out.txt", "cli cli|cli cli cli"},
		{"pattern append then override append command line quirk", "%.txt: CFLAGS += A\n%.txt: override CFLAGS += B\none.txt:\n\tprintf '%s|%s|%s' '$(value CFLAGS)' '$(origin CFLAGS)' '$(CFLAGS)' > out.txt\n", map[string]string{"CFLAGS": "cli"}, "one.txt", "out.txt", "cli B|override|cli cli B"},
		{"more specific pattern wins regardless of order", "special%.txt: MODE = specific\n%.txt: MODE = generic\nspecial1.txt:\n\tprintf '%s' '$(MODE)' > out.txt\n", nil, "special1.txt", "out.txt", "specific"},
		{"equal stem uses later definition", "sp%1.txt: MODE = first\nspe%.txt: MODE = second\nspecial1.txt:\n\tprintf '%s' '$(MODE)' > out.txt\n", nil, "special1.txt", "out.txt", "second"},
		{"eval top-level assignment", "$(eval X := hi)\nall:\n\tprintf '%s' '$(X)' > out.txt\n", nil, "all", "out.txt", "hi"},
		{"eval retains hash in function args", "$(eval X := hi #comment)\nall:\n\tprintf '%s' '$(X)' > out.txt\n", nil, "all", "out.txt", "hi "},
		{"eval generated assignment from call", "GEN = Z := $(1)\n$(eval $(call GEN,ok))\nall:\n\tprintf '%s' '$(Z)' > out.txt\n", nil, "all", "out.txt", "ok"},
		{"eval assignment operator from first equals", "DEF = Y := $(X)\nX = hi\n$(eval $(DEF))\nall:\n\tprintf '%s' '$(Y)' > out.txt\n", nil, "all", "out.txt", "hi"},
		{"single-char ref with escaped shell var", "X = make\nall:\n\tX=shell; printf '%s|%s' '$X' \"$${X}\" > out.txt\n", nil, "all", "out.txt", "make|shell"},
		{"private variable hidden from recipe", "MODE = debug\nprivate MODE := release\nMSG := $(MODE)\nall:\n\tprintf '[%s]' '$(MODE)' > out.txt\n", nil, "all", "out.txt", "[]"},
		{"undefine clears private state", "private MODE := release\nundefine MODE\nMODE = debug\nall:\n\tprintf '%s' '$(MODE)' > out.txt\n", nil, "all", "out.txt", "debug"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recipeOutput(t, tc.makefile, tc.overrides, tc.target, tc.out); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}
