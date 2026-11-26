package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sourcedScriptPattern captures the argument of the `source` shell builtin.
// Quoted arguments are captured as a whole so that paths with spaces survive.
var sourcedScriptPattern = regexp.MustCompile(`\bsource\s+("[^"]*"|'[^']*'|[^\s;&|<>()]+)`)

// checkSourcedScripts allows `source <file>` only when the file resolves inside
// the workspace directory. Sourcing runs the file in the current shell, so an
// arbitrary path is code execution with the agent's privileges; a script that
// lives inside the workspace is something the agent could just run anyway.
//
// Dynamic paths (`$VAR`, backticks, globs, `~`) and unknown flags fail closed:
// the guard cannot know what they expand to.
func checkSourcedScripts(command, cwd string) string {
	matches := sourcedScriptPattern.FindAllStringSubmatch(command, -1)
	if len(matches) == 0 {
		return ""
	}

	root := cwd
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "Command blocked by safety guard (cannot resolve workspace for sourced script)"
		}
		root = wd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "Command blocked by safety guard (cannot resolve workspace for sourced script)"
	}
	absRoot = filepath.Clean(absRoot)

	for _, m := range matches {
		arg := strings.Trim(m[1], `"'`)
		if arg == "" || strings.HasPrefix(arg, "-") ||
			strings.ContainsAny(arg, "$`*?~") {
			return "Command blocked by safety guard (sourced script path must be literal)"
		}

		p := arg
		if !filepath.IsAbs(p) {
			p = filepath.Join(absRoot, p)
		}
		p = filepath.Clean(p)

		if p != absRoot && !strings.HasPrefix(p, absRoot+string(os.PathSeparator)) {
			return fmt.Sprintf(
				"Command blocked by safety guard (sourced script outside workspace: %s)", arg)
		}
	}
	return ""
}
