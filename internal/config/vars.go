package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	protectedDollar                    = "\x01"
	originPrefix                       = ".GOMAKE_ORIGIN."
	patternCommandLineAppendBasePrefix = ".GOMAKE_PATTERN_CLI_APPEND_BASE."
	patternCommandLineAppendSeedPrefix = ".GOMAKE_PATTERN_CLI_APPEND_SEED."
)

func expandVars(input string, vars map[string]string, flavors map[string]VariableFlavor) string {
	return expandVarsWithStack(input, vars, flavors, nil, nil)
}

func expandVarsWarnUndefined(input string, vars map[string]string, flavors map[string]VariableFlavor, onUndefined func(string)) string {
	return expandVarsWithStack(input, vars, flavors, nil, onUndefined)
}

func expandRecipeVars(input string, vars map[string]string, flavors map[string]VariableFlavor) string {
	return ExpandVarsWithOptions(input, vars, flavors, nil, true, true, nil, nil)
}

func expandRecipeVarsWarnUndefined(input string, vars map[string]string, flavors map[string]VariableFlavor, onUndefined func(string)) string {
	return ExpandVarsWithOptions(input, vars, flavors, nil, true, true, onUndefined, nil)
}

func expandRecipeVarsWithCallbacks(input string, vars map[string]string, flavors map[string]VariableFlavor, onUndefined func(string), onRecursive func(string)) string {
	return ExpandVarsWithOptions(input, vars, flavors, nil, true, true, onUndefined, onRecursive)
}

func expandVarsWithStack(input string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, onUndefined func(string)) string {
	return expandVarsWithStackCallbacks(input, vars, flavors, stack, onUndefined, nil)
}

func expandVarsWithStackCallbacks(input string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, onUndefined func(string), onRecursive func(string)) string {
	return ExpandVarsWithOptions(input, vars, flavors, stack, true, false, onUndefined, onRecursive)
}

func ExpandVarsWithOptions(input string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, expandSingleChar bool, preserveAutomatic bool, onUndefined func(string), onRecursive func(string)) string {
	result := input
	for {
		start, end, kind, ok := findVarRef(result, expandSingleChar)
		if !ok {
			return strings.ReplaceAll(result, protectedDollar, "$")
		}
		token := result[start:end]
		replacement := ""
		if kind == '(' || kind == '{' {
			expr := strings.TrimSpace(token[2 : len(token)-1])
			if value, handled := evalFunction(expr, vars, flavors, stack, preserveAutomatic, onUndefined, onRecursive); handled {
				replacement = value
			} else {
				key := expandVarsWithStackCallbacks(expr, vars, flavors, stack, onUndefined, onRecursive)
				if preserveAutomatic && isAutomaticReference(key) {
					replacement = protectDollars(token)
				} else {
					replacement = resolveVar(key, vars, flavors, stack, onUndefined, onRecursive)
					if flavors[key] == FlavorSimple {
						replacement = protectDollars(replacement)
					}
				}
			}
		} else {
			if kind == '$' {
				replacement = protectedDollar
				result = result[:start] + replacement + result[end:]
				continue
			}
			key := variableRefName(token, kind, vars, flavors, stack)
			if preserveAutomatic && isAutomaticReference(key) {
				replacement = protectDollars(token)
			} else {
				replacement = resolveVar(key, vars, flavors, stack, onUndefined, onRecursive)
				if flavors[key] == FlavorSimple {
					replacement = protectDollars(replacement)
				}
			}
		}
		result = result[:start] + replacement + result[end:]
	}
}

func evalFunction(expr string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, preserveAutomatic bool, onUndefined func(string), onRecursive func(string)) (string, bool) {
	name, args, ok := splitFunctionInvocation(expr)
	if !ok {
		return "", false
	}

	expand := func(input string) string {
		return ExpandVarsWithOptions(input, vars, flavors, stack, true, preserveAutomatic, nil, onRecursive)
	}

	switch name {
	case "subst":
		parts := splitFunctionArgs(args)
		if len(parts) != 3 {
			return "", true
		}
		from := expand(parts[0])
		to := expand(parts[1])
		text := expand(parts[2])
		if from == "" {
			return text, true
		}
		return strings.ReplaceAll(text, from, to), true
	case "patsubst":
		parts := splitFunctionArgs(args)
		if len(parts) != 3 {
			return "", true
		}
		pattern := expand(parts[0])
		replacement := expand(parts[1])
		words := strings.Fields(expand(parts[2]))
		out := make([]string, 0, len(words))
		for _, word := range words {
			if value, ok := patsubstWord(pattern, replacement, word); ok {
				out = append(out, value)
				continue
			}
			out = append(out, word)
		}
		return strings.Join(out, " "), true
	case "strip":
		return strings.Join(strings.Fields(expand(args)), " "), true
	case "findstring":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		needle := expand(parts[0])
		haystack := expand(parts[1])
		if strings.Contains(haystack, needle) {
			return needle, true
		}
		return "", true
	case "filter", "filter-out":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		patterns := strings.Fields(expand(parts[0]))
		words := strings.Fields(expand(parts[1]))
		var out []string
		for _, word := range words {
			matched := false
			for _, pattern := range patterns {
				if matchMakePattern(pattern, word) {
					matched = true
					break
				}
			}
			if name == "filter" && matched {
				out = append(out, word)
			}
			if name == "filter-out" && !matched {
				out = append(out, word)
			}
		}
		return strings.Join(out, " "), true
	case "sort":
		words := strings.Fields(expand(args))
		if len(words) == 0 {
			return "", true
		}
		set := make(map[string]struct{}, len(words))
		for _, word := range words {
			set[word] = struct{}{}
		}
		out := make([]string, 0, len(set))
		for word := range set {
			out = append(out, word)
		}
		sort.Strings(out)
		return strings.Join(out, " "), true
	case "word":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		n, err := strconv.Atoi(strings.TrimSpace(expand(parts[0])))
		if err != nil || n <= 0 {
			return "", true
		}
		words := strings.Fields(expand(parts[1]))
		if n > len(words) {
			return "", true
		}
		return words[n-1], true
	case "wordlist":
		parts := splitFunctionArgs(args)
		if len(parts) != 3 {
			return "", true
		}
		start, errStart := strconv.Atoi(strings.TrimSpace(expand(parts[0])))
		end, errEnd := strconv.Atoi(strings.TrimSpace(expand(parts[1])))
		if errStart != nil || errEnd != nil || start <= 0 || end < start {
			return "", true
		}
		words := strings.Fields(expand(parts[2]))
		if start > len(words) {
			return "", true
		}
		if end > len(words) {
			end = len(words)
		}
		return strings.Join(words[start-1:end], " "), true
	case "words":
		return strconv.Itoa(len(strings.Fields(expand(args)))), true
	case "firstword":
		words := strings.Fields(expand(args))
		if len(words) == 0 {
			return "", true
		}
		return words[0], true
	case "lastword":
		words := strings.Fields(expand(args))
		if len(words) == 0 {
			return "", true
		}
		return words[len(words)-1], true
	case "join":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		left := strings.Fields(expand(parts[0]))
		right := strings.Fields(expand(parts[1]))
		size := len(left)
		if len(right) > size {
			size = len(right)
		}
		out := make([]string, 0, size)
		for idx := 0; idx < size; idx++ {
			var a, b string
			if idx < len(left) {
				a = left[idx]
			}
			if idx < len(right) {
				b = right[idx]
			}
			out = append(out, a+b)
		}
		return strings.Join(out, " "), true
	case "dir":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			dir := filepath.Dir(word)
			if dir == "." {
				dir = "./"
			} else if !strings.HasSuffix(dir, "/") {
				dir += "/"
			}
			out = append(out, dir)
		}
		return strings.Join(out, " "), true
	case "notdir":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			out = append(out, filepath.Base(word))
		}
		return strings.Join(out, " "), true
	case "basename":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			suffix := wordSuffix(word)
			out = append(out, strings.TrimSuffix(word, suffix))
		}
		return strings.Join(out, " "), true
	case "suffix":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			suffix := wordSuffix(word)
			if suffix != "" {
				out = append(out, suffix)
			}
		}
		return strings.Join(out, " "), true
	case "addprefix":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		prefix := expand(parts[0])
		words := strings.Fields(expand(parts[1]))
		out := make([]string, 0, len(words))
		for _, word := range words {
			out = append(out, prefix+word)
		}
		return strings.Join(out, " "), true
	case "addsuffix":
		parts := splitFunctionArgs(args)
		if len(parts) != 2 {
			return "", true
		}
		suffix := expand(parts[0])
		words := strings.Fields(expand(parts[1]))
		out := make([]string, 0, len(words))
		for _, word := range words {
			out = append(out, word+suffix)
		}
		return strings.Join(out, " "), true
	case "abspath":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			if abs, err := filepath.Abs(word); err == nil {
				out = append(out, abs)
			}
		}
		return strings.Join(out, " "), true
	case "realpath":
		words := strings.Fields(expand(args))
		out := make([]string, 0, len(words))
		for _, word := range words {
			resolved, err := filepath.EvalSymlinks(word)
			if err != nil {
				continue
			}
			abs, err := filepath.Abs(resolved)
			if err != nil {
				continue
			}
			out = append(out, abs)
		}
		return strings.Join(out, " "), true
	case "wildcard":
		patterns := strings.Fields(expand(args))
		baseDir := vars["CURDIR"]
		if strings.TrimSpace(baseDir) == "" {
			baseDir = "."
		}
		var out []string
		for _, pattern := range patterns {
			matches := wildcardMatches(baseDir, pattern)
			if len(matches) == 0 {
				continue
			}
			out = append(out, matches...)
		}
		return strings.Join(out, " "), true
	case "if":
		parts := splitFunctionArgs(args)
		if len(parts) < 2 {
			return "", true
		}
		if strings.TrimSpace(expand(parts[0])) != "" {
			return expand(parts[1]), true
		}
		if len(parts) >= 3 {
			return expand(parts[2]), true
		}
		return "", true
	case "or":
		parts := splitFunctionArgs(args)
		for _, part := range parts {
			value := expand(part)
			if strings.TrimSpace(value) != "" {
				return value, true
			}
		}
		return "", true
	case "and":
		parts := splitFunctionArgs(args)
		last := ""
		for _, part := range parts {
			last = expand(part)
			if strings.TrimSpace(last) == "" {
				return "", true
			}
		}
		return last, true
	case "foreach":
		parts := splitFunctionArgs(args)
		if len(parts) != 3 {
			return "", true
		}
		name := strings.TrimSpace(expand(parts[0]))
		if name == "" {
			return "", true
		}
		words := strings.Fields(expand(parts[1]))
		saved, hadSaved := saveVariable(vars, flavors, name)
		out := make([]string, 0, len(words))
		for _, word := range words {
			vars[name] = word
			flavors[name] = FlavorSimple
			out = append(out, expand(parts[2]))
		}
		restoreVariable(vars, flavors, name, saved, hadSaved)
		return strings.Join(out, " "), true
	case "call":
		parts := splitFunctionArgs(args)
		if len(parts) == 0 {
			return "", true
		}
		callee := strings.TrimSpace(expand(parts[0]))
		if callee == "" {
			return "", true
		}

		type savedBinding struct {
			name  string
			value string
			flv   VariableFlavor
			had   bool
		}
		saved := make([]savedBinding, 0, len(parts)+1)
		setTemp := func(name, value string) {
			current, had := saveVariable(vars, flavors, name)
			saved = append(saved, savedBinding{name: name, value: current.value, flv: current.flavor, had: had})
			vars[name] = value
			flavors[name] = FlavorSimple
		}

		setTemp("0", callee)
		for idx := 1; idx < len(parts); idx++ {
			setTemp(strconv.Itoa(idx), expand(parts[idx]))
		}

		defer func() {
			for idx := len(saved) - 1; idx >= 0; idx-- {
				item := saved[idx]
				restoreVariable(vars, flavors, item.name, variableBinding{value: item.value, flavor: item.flv}, item.had)
			}
		}()

		if body, ok := vars[callee]; ok {
			return expand(body), true
		}

		if !isSupportedFunction(callee) {
			return "", true
		}
		invocation := callee
		if len(parts) > 1 {
			invocation += " " + strings.Join(parts[1:], ",")
		}
		value, _ := evalFunction(invocation, vars, flavors, stack, preserveAutomatic, onUndefined, onRecursive)
		return value, true
	case "origin":
		name := strings.TrimSpace(expand(args))
		if name == "" {
			return "undefined", true
		}
		if origin := getVariableOrigin(vars, name); origin != "" {
			return origin, true
		}
		if _, ok := vars[name]; !ok {
			return "undefined", true
		}
		if isBuiltinVariable(name) {
			return "default", true
		}
		return "file", true
	case "value":
		name := strings.TrimSpace(expand(args))
		return protectDollars(vars[name]), true
	case "flavor":
		name := strings.TrimSpace(expand(args))
		flavor, ok := flavors[name]
		if !ok {
			return "undefined", true
		}
		if flavor == FlavorSimple {
			return "simple", true
		}
		return "recursive", true
	case "shell":
		return runShellFunction(expand(args), vars), true
	case "file":
		return evalFileFunction(args, vars, flavors, stack, preserveAutomatic, onRecursive), true
	case "info":
		fmt.Fprintln(os.Stdout, expand(args))
		return "", true
	case "warning":
		fmt.Fprintln(os.Stderr, expand(args))
		return "", true
	case "error":
		fmt.Fprintln(os.Stderr, expand(args))
		return "", true
	case "eval":
		text := expand(args)
		for _, line := range strings.Split(text, "\n") {
			stripped := stripComment(line)
			if strings.TrimSpace(stripped) == "" {
				continue
			}
			candidate := strings.TrimLeft(stripped, " \t")
			if key, value, operator, ok := parseAssignment(candidate); ok {
				if err := applyAssignment(vars, flavors, key, value, operator); err != nil {
					continue
				}
				setVariableOrigin(vars, key, "file")
			}
		}
		return "", true
	default:
		return "", false
	}
}

func splitFunctionInvocation(expr string) (string, string, bool) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return "", "", false
	}
	idx := strings.IndexAny(trimmed, " \t")
	if idx <= 0 {
		return "", "", false
	}
	name := trimmed[:idx]
	if !isSupportedFunction(name) {
		return "", "", false
	}
	return name, strings.TrimSpace(trimmed[idx+1:]), true
}

func isSupportedFunction(name string) bool {
	switch name {
	case "subst", "patsubst", "strip", "findstring", "filter", "filter-out", "sort",
		"word", "wordlist", "words", "firstword", "lastword", "join",
		"dir", "notdir", "basename", "suffix", "addprefix", "addsuffix",
		"abspath", "realpath", "wildcard", "if", "or", "and", "foreach",
		"call", "value", "origin", "flavor", "eval", "shell", "file",
		"error", "warning", "info":
		return true
	default:
		return false
	}
}

func splitFunctionArgs(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var out []string
	start := 0
	depthParen := 0
	depthBrace := 0
	for idx := 0; idx < len(input); idx++ {
		switch input[idx] {
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case ',':
			if depthParen == 0 && depthBrace == 0 {
				out = append(out, strings.TrimSpace(input[start:idx]))
				start = idx + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(input[start:]))
	return out
}

func matchMakePattern(pattern, word string) bool {
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		return pattern == word
	}
	prefix := pattern[:idx]
	suffix := pattern[idx+1:]
	if !strings.HasPrefix(word, prefix) || !strings.HasSuffix(word, suffix) {
		return false
	}
	return len(word) >= len(prefix)+len(suffix)
}

func wordSuffix(word string) string {
	base := filepath.Base(word)
	idx := strings.LastIndexByte(base, '.')
	if idx <= 0 {
		return ""
	}
	return base[idx:]
}

func protectDollars(input string) string {
	return strings.ReplaceAll(input, "$", protectedDollar)
}

type variableBinding struct {
	value  string
	flavor VariableFlavor
}

func saveVariable(vars map[string]string, flavors map[string]VariableFlavor, name string) (variableBinding, bool) {
	value, ok := vars[name]
	if !ok {
		return variableBinding{}, false
	}
	return variableBinding{value: value, flavor: flavors[name]}, true
}

func restoreVariable(vars map[string]string, flavors map[string]VariableFlavor, name string, binding variableBinding, had bool) {
	if !had {
		delete(vars, name)
		delete(flavors, name)
		delete(vars, originPrefix+name)
		return
	}
	vars[name] = binding.value
	flavors[name] = binding.flavor
}

func setVariableOrigin(vars map[string]string, name, origin string) {
	if strings.HasPrefix(name, originPrefix) {
		return
	}
	vars[originPrefix+name] = origin
}

func getVariableOrigin(vars map[string]string, name string) string {
	if strings.HasPrefix(name, originPrefix) {
		return ""
	}
	return vars[originPrefix+name]
}

func patsubstWord(pattern, replacement, word string) (string, bool) {
	idx := strings.Index(pattern, "%")
	if idx < 0 {
		if pattern != word {
			return "", false
		}
		return replacement, true
	}

	prefix := pattern[:idx]
	suffix := pattern[idx+1:]
	if !strings.HasPrefix(word, prefix) || !strings.HasSuffix(word, suffix) {
		return "", false
	}
	if len(word) < len(prefix)+len(suffix) {
		return "", false
	}
	stem := word[len(prefix) : len(word)-len(suffix)]
	return strings.Replace(replacement, "%", stem, 1), true
}

func wildcardMatches(baseDir, pattern string) []string {
	if pattern == "" {
		return nil
	}
	globPattern := pattern
	if !filepath.IsAbs(pattern) {
		globPattern = filepath.Join(baseDir, pattern)
	}
	matches, err := filepath.Glob(globPattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if filepath.IsAbs(pattern) {
			out = append(out, match)
			continue
		}
		rel, err := filepath.Rel(baseDir, match)
		if err != nil {
			continue
		}
		out = append(out, rel)
	}
	return out
}

func isBuiltinVariable(name string) bool {
	switch name {
	case "CURDIR", "MAKE", "MAKECMDGOALS", "MAKEFILE_LIST", ".SHELLFLAGS", ".DEFAULT_GOAL", ".RECIPEPREFIX":
		return true
	default:
		return false
	}
}

func runShellFunction(command string, vars map[string]string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	shell := strings.TrimSpace(vars["SHELL"])
	if shell == "" {
		shell = "/bin/sh"
	}
	args := splitShellFlags(vars[".SHELLFLAGS"])
	args = append(args, command)

	cmd := exec.Command(shell, args...)
	if dir := strings.TrimSpace(vars["CURDIR"]); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeFunctionOutput(string(output))
}

func splitShellFlags(value string) []string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return []string{"-c"}
	}
	return parts
}

func evalFileFunction(args string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, preserveAutomatic bool, onRecursive func(string)) string {
	parts := splitFunctionArgs(args)
	if len(parts) == 0 {
		return ""
	}
	expand := func(input string) string {
		return ExpandVarsWithOptions(input, vars, flavors, stack, true, preserveAutomatic, nil, onRecursive)
	}

	baseDir := vars["CURDIR"]
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	opArg := strings.TrimSpace(expand(parts[0]))

	switch {
	case strings.HasPrefix(opArg, "<<"):
		return ""
	case strings.HasPrefix(opArg, ">>"):
		path := resolveFunctionPath(baseDir, strings.TrimSpace(opArg[2:]))
		if path == "" {
			return ""
		}
		text := ""
		if len(parts) >= 2 {
			text = expand(parts[1])
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ""
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return ""
		}
		defer file.Close()
		_, _ = file.WriteString(text)
		return ""
	case strings.HasPrefix(opArg, ">"):
		path := resolveFunctionPath(baseDir, strings.TrimSpace(opArg[1:]))
		if path == "" {
			return ""
		}
		text := ""
		if len(parts) >= 2 {
			text = expand(parts[1])
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ""
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return ""
		}
		return ""
	case strings.HasPrefix(opArg, "<"):
		path := resolveFunctionPath(baseDir, strings.TrimSpace(opArg[1:]))
		if path == "" {
			return ""
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return normalizeFunctionOutput(string(content))
	default:
		return ""
	}
}

func resolveFunctionPath(baseDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

func normalizeFunctionOutput(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.TrimRight(value, "\n")
	return strings.ReplaceAll(value, "\n", " ")
}

func isAutomaticReference(name string) bool {
	if len(name) == 1 {
		switch name[0] {
		case '@', '<', '^', '+', '|', '?', '*':
			return true
		}
		return false
	}
	if len(name) != 2 {
		return false
	}
	if name[1] != 'D' && name[1] != 'F' {
		return false
	}
	switch name[0] {
	case '@', '<', '^', '+', '?', '*':
		return true
	default:
		return false
	}
}

func findVarRef(input string, expandSingleChar bool) (int, int, byte, bool) {
	for start := 0; start < len(input)-1; start++ {
		if input[start] != '$' {
			continue
		}
		switch input[start+1] {
		case '(':
			end, ok := findDelimitedVarEnd(input, start+2, '(', ')')
			if ok {
				return start, end, '(', true
			}
			return -1, -1, 0, false
		case '{':
			end, ok := findDelimitedVarEnd(input, start+2, '{', '}')
			if ok {
				return start, end, '{', true
			}
			return -1, -1, 0, false
		default:
			if !expandSingleChar {
				continue
			}
			return start, start + 2, input[start+1], true
		}
	}
	return -1, -1, 0, false
}

func findDelimitedVarEnd(input string, start int, open, close byte) (int, bool) {
	depth := 1
	for idx := start; idx < len(input); idx++ {
		switch input[idx] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return idx + 1, true
			}
		}
	}
	return -1, false
}

func variableRefName(token string, kind byte, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool) string {
	switch kind {
	default:
		return string(kind)
	}
}

func resolveVar(name string, vars map[string]string, flavors map[string]VariableFlavor, stack map[string]bool, onUndefined func(string), onRecursive func(string)) string {
	value, ok := vars[name]
	if !ok {
		if onUndefined != nil && name != "" {
			onUndefined(name)
		}
		return ""
	}
	if stack == nil {
		stack = map[string]bool{}
	}
	if stack[name] {
		if onRecursive != nil && name != "" {
			onRecursive(name)
		}
		return ""
	}
	appendBase, appendBaseSet := vars[patternCommandLineAppendBasePrefix+name]
	appendSeed := vars[patternCommandLineAppendSeedPrefix+name]
	if flavors[name] == FlavorSimple {
		return applyPatternCommandLineAppendQuirk(value, appendBaseSet, appendBase, appendSeed)
	}
	next := cloneStack(stack)
	next[name] = true
	expanded := expandVarsWithStackCallbacks(value, vars, flavors, next, onUndefined, onRecursive)
	return applyPatternCommandLineAppendQuirk(expanded, appendBaseSet, appendBase, appendSeed)
}

func applyPatternCommandLineAppendQuirk(value string, appendBaseSet bool, appendBase, appendSeed string) string {
	if !appendBaseSet || appendBase == "" || appendSeed == "" {
		return value
	}
	if value == appendSeed {
		return appendSeed + " " + appendBase
	}
	prefix := appendSeed + " "
	if strings.HasPrefix(value, prefix) {
		return appendSeed + " " + appendBase + value[len(appendSeed):]
	}
	return value
}

func cloneStack(stack map[string]bool) map[string]bool {
	out := make(map[string]bool, len(stack)+1)
	for key, value := range stack {
		out[key] = value
	}
	return out
}

func applyAssignment(vars map[string]string, flavors map[string]VariableFlavor, key, value, operator string) error {
	return applyAssignmentWithWarning(vars, flavors, key, value, operator, nil)
}

func applyAssignmentWithWarning(vars map[string]string, flavors map[string]VariableFlavor, key, value, operator string, onUndefined func(string)) error {
	switch operator {
	case "=":
		vars[key] = value
		flavors[key] = FlavorRecursive
	case ":=", "::=":
		vars[key] = expandVarsWarnUndefined(value, vars, flavors, onUndefined)
		flavors[key] = FlavorSimple
	case "!=":
		vars[key] = runShellFunction(expandVarsWarnUndefined(value, vars, flavors, onUndefined), vars)
		flavors[key] = FlavorRecursive
	case "?=":
		if _, ok := vars[key]; !ok {
			vars[key] = value
			flavors[key] = FlavorRecursive
		}
	case "+=":
		appendValueWithWarning(vars, flavors, key, value, onUndefined)
	default:
		return fmt.Errorf("unsupported assignment operator %q", operator)
	}

	return nil
}

func appendValue(vars map[string]string, flavors map[string]VariableFlavor, key, value string) {
	appendValueWithWarning(vars, flavors, key, value, nil)
}

func appendValueWithWarning(vars map[string]string, flavors map[string]VariableFlavor, key, value string, onUndefined func(string)) {
	current, exists := vars[key]
	if !exists {
		vars[key] = value
		flavors[key] = FlavorRecursive
		return
	}

	switch flavors[key] {
	case FlavorSimple:
		if current != "" {
			vars[key] = current + " " + expandVarsWarnUndefined(value, vars, flavors, onUndefined)
			return
		}
		vars[key] = expandVarsWarnUndefined(value, vars, flavors, onUndefined)
	default:
		if current != "" {
			vars[key] = current + " " + value
			return
		}
		vars[key] = value
	}
}

func snapshotVariables(vars map[string]string, flavors map[string]VariableFlavor) map[string]string {
	snapshot := make(map[string]string, len(vars))
	for key := range vars {
		if strings.HasPrefix(key, originPrefix) {
			continue
		}
		snapshot[key] = resolveVar(key, vars, flavors, nil, nil, nil)
	}
	return snapshot
}

func evaluateIfEq(expr string, vars map[string]string, flavors map[string]VariableFlavor, equal bool) (bool, error) {
	left, right, err := parseIfEqArgs(expr)
	if err != nil {
		return false, err
	}
	match := expandVars(left, vars, flavors) == expandVars(right, vars, flavors)
	if equal {
		return match, nil
	}
	return !match, nil
}

func parseIfEqArgs(expr string) (string, string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", "", fmt.Errorf("unsupported conditional syntax %q", expr)
	}
	if expr[0] == '(' {
		return parseParenthesizedIfEqArgs(expr)
	}
	return parseQuotedIfEqArgs(expr)
}

func parseParenthesizedIfEqArgs(expr string) (string, string, error) {
	if expr[len(expr)-1] != ')' {
		return "", "", fmt.Errorf("unsupported conditional syntax %q", expr)
	}
	body := expr[1 : len(expr)-1]
	split := splitTopLevelComma(body)
	if split == -1 {
		return "", "", fmt.Errorf("unsupported conditional syntax %q", expr)
	}
	return strings.TrimSpace(body[:split]), strings.TrimSpace(body[split+1:]), nil
}

func splitTopLevelComma(input string) int {
	depth := 0
	var quote byte
	for idx := 0; idx < len(input); idx++ {
		ch := input[idx]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return idx
			}
		}
	}
	return -1
}

func parseQuotedIfEqArgs(expr string) (string, string, error) {
	left, rest, err := parseQuotedToken(expr)
	if err != nil {
		return "", "", fmt.Errorf("unsupported conditional syntax %q", expr)
	}
	right, tail, err := parseQuotedToken(strings.TrimSpace(rest))
	if err != nil || strings.TrimSpace(tail) != "" {
		return "", "", fmt.Errorf("unsupported conditional syntax %q", expr)
	}
	return left, right, nil
}

func parseQuotedToken(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if len(input) < 2 {
		return "", "", fmt.Errorf("invalid token")
	}
	quote := input[0]
	if quote != '\'' && quote != '"' {
		return "", "", fmt.Errorf("invalid token")
	}
	for idx := 1; idx < len(input); idx++ {
		if input[idx] == quote {
			return input[1:idx], input[idx+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid token")
}
