package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type shellExitError struct {
	err error
}

func (e *shellExitError) Error() string {
	return e.err.Error()
}

func (e *shellExitError) Unwrap() error {
	return e.err
}

func runShellCommand(dir, shell, shellFlags string, env []string, command string) error {
	if strings.TrimSpace(shell) == "" {
		return runEmbeddedShellCommand(dir, env, command)
	}
	return runExternalShellCommand(dir, shell, shellFlags, env, command)
}

func runEmbeddedShellCommand(dir string, env []string, command string) error {
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return fmt.Errorf("parse shell command: %w", err)
	}
	if len(env) == 0 {
		env = os.Environ()
	}

	runner, err := interp.New(
		interp.Dir(dir),
		interp.Env(expand.ListEnviron(env...)),
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
	)
	if err != nil {
		return fmt.Errorf("compose shell command: %w", err)
	}

	if err := runner.Run(context.Background(), prog); err != nil {
		if _, ok := interp.IsExitStatus(err); ok {
			return &shellExitError{err: fmt.Errorf("shell command failed: %w", err)}
		}
		return fmt.Errorf("shell command failed: %w", err)
	}
	return nil
}

func runExternalShellCommand(dir, shell, shellFlags string, env []string, command string) error {
	args := parseShellFlags(shellFlags)
	args = append(args, command)
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	if len(env) == 0 {
		env = os.Environ()
	}
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &shellExitError{err: fmt.Errorf("shell command failed: %w", err)}
		}
		return fmt.Errorf("start shell %q: %w", shell, err)
	}
	return nil
}

func parseShellFlags(flags string) []string {
	parts := strings.Fields(flags)
	if len(parts) == 0 {
		return []string{"-c"}
	}
	return parts
}
