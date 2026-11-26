package tools

import (
	"regexp"
	"strings"
)

// Inline-command (substitution) verification for the exec safety guard.
//
// Why this file exists: the guard used to block every `$(...)`, `${...}` and
// backtick outright, by plain text match. That stopped both dangerous and
// harmless commands alike, e.g. `grep -n "$(date)" file` or `echo "$(pwd)"`.
//
// The rule now: an inline command is allowed only if its body passes exactly
// the same deny checks as a top-level command, and the nesting is bounded.
// Everything we cannot parse (unbalanced quotes/parens, too deep, too long)
// fails closed — blocked.
//
// This is still a heuristic guard, not a sandbox: it prevents the common
// accidents and the obvious escapes, and deliberately stays conservative on
// anything it does not understand.

const (
	// maxSubstitutionDepth bounds recursion for nested substitutions like
	// `$(echo $(date))`.
	maxSubstitutionDepth = 3
	// maxSubstitutionInput bounds the string we are willing to parse.
	maxSubstitutionInput = 8192
)

// simpleVarPattern accepts only plain variable names inside `${...}`.
// `${VAR}` is fine, `${VAR:-$(rm -rf /)}` is not: parameter expansion with
// operators can embed commands, so it is rejected instead of half-parsed.
var simpleVarPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// blockedInline reports an inline command the guard refuses to verify.
func blockedInline(reason, snippet string) string {
	snippet = strings.TrimSpace(snippet)
	if len(snippet) > 60 {
		snippet = snippet[:60] + "…"
	}
	if snippet == "" {
		return "Command blocked by safety guard (inline command " + reason + ")"
	}
	return "Command blocked by safety guard (inline command " + reason + ": " + snippet + ")"
}

// checkInlineCommands verifies every `$(...)`, backtick substitution and
// `${...}` expansion in command. An empty return means the command passed.
func checkInlineCommands(command string, denyPatterns []*regexp.Regexp) string {
	return scanInlineCommands(command, denyPatterns, 0)
}

func scanInlineCommands(s string, deny []*regexp.Regexp, depth int) string {
	if depth > maxSubstitutionDepth {
		return blockedInline("nested too deeply", "")
	}
	if len(s) > maxSubstitutionInput {
		return blockedInline("too long to verify", "")
	}

	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "$("):
			inner, next, ok := scanBalancedParens(s, i+2)
			if !ok {
				return blockedInline("has unbalanced $(", s[i:])
			}
			if msg := verifyInlineBody(inner, deny, depth); msg != "" {
				return msg
			}
			i = next - 1
		case s[i] == '`':
			inner, next, ok := scanUntilBacktick(s, i+1)
			if !ok {
				return blockedInline("has unbalanced backtick", s[i:])
			}
			if msg := verifyInlineBody(inner, deny, depth); msg != "" {
				return msg
			}
			i = next - 1
		case strings.HasPrefix(s[i:], "${"):
			inner, next, ok := scanBalancedBraces(s, i+2)
			if !ok {
				return blockedInline("has unbalanced ${", s[i:])
			}
			if !simpleVarPattern.MatchString(strings.ToLower(strings.TrimSpace(inner))) {
				return blockedInline("uses non-trivial parameter expansion", "${"+inner+"}")
			}
			i = next - 1
		}
	}

	return ""
}

// verifyInlineBody applies the deny list to the body of an inline command and
// recurses into substitutions it may contain.
func verifyInlineBody(inner string, deny []*regexp.Regexp, depth int) string {
	body := strings.TrimSpace(inner)
	if body == "" {
		return blockedInline("is empty", "")
	}

	for _, pattern := range deny {
		if pattern.MatchString(body) {
			return "Command blocked by safety guard (dangerous pattern detected in inline command: " +
				shorten(body, 60) + ")"
		}
	}

	return scanInlineCommands(body, deny, depth+1)
}

func shorten(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// scanBalancedParens reads from start until the matching ')', tolerating nested
// parentheses so `$(echo $(pwd))` is parsed as one body.
func scanBalancedParens(s string, start int) (string, int, bool) {
	level := 1
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '(':
			level++
		case ')':
			level--
			if level == 0 {
				return s[start:i], i + 1, true
			}
		}
	}
	return "", 0, false
}

// scanUntilBacktick reads from start until the closing backtick.
func scanUntilBacktick(s string, start int) (string, int, bool) {
	for i := start; i < len(s); i++ {
		if s[i] == '`' {
			return s[start:i], i + 1, true
		}
	}
	return "", 0, false
}

// scanBalancedBraces reads from start until the matching '}', tolerating nested
// `${...}` forms.
func scanBalancedBraces(s string, start int) (string, int, bool) {
	level := 1
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			level++
		case '}':
			level--
			if level == 0 {
				return s[start:i], i + 1, true
			}
		}
	}
	return "", 0, false
}
