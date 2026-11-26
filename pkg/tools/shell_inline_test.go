package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Inline commands are now parsed and their bodies re-checked, instead of being
// blanket-blocked. These tests pin both halves of that contract: harmless
// substitution passes, dangerous content inside it still gets caught, and the
// parser fails closed on anything it cannot read.

func TestGuardInlineCommandsAllowsHarmlessSubstitution(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("NewExecTool() error: %v", err)
	}

	commands := []string{
		`echo "$(date)"`,
		`echo "$(pwd)"`,
		`grep -n "$(date +%F)" memory/notes.md`,
		`echo "$(echo $(pwd))"`,
		`ls -la ${HOME}/tmp`,
		`echo "${PICOCLAW_HOME}"`,
		`git log --oneline -1 "$(git rev-parse --abbrev-ref head)"`,
		`awk '{print $1}' file.txt`,
		"echo `date`",
	}

	for _, cmd := range commands {
		if got := tool.guardCommand(cmd, t.TempDir()); got != "" {
			t.Errorf("harmless inline command should pass guard: %s\n  guard said: %s", cmd, got)
		}
	}
}

func TestGuardInlineCommandsBlocksDangerousBody(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("NewExecTool() error: %v", err)
	}

	commands := []string{
		`echo "$(rm -rf /root/backups)"`,
		"echo `rm -rf /root/backups`",
		`echo "$(chown root:root /root/x)"`,
		`echo "$(git push origin master)"`,
		`echo "${HOME:-$(rm -rf /root/backups)}"`,
		// no bypass through quotes: the deny list still sees the whole text
		`bash -c "rm -rf /root/backups"`,
	}

	for _, cmd := range commands {
		got := tool.guardCommand(cmd, t.TempDir())
		if got == "" {
			t.Errorf("dangerous inline command must be blocked: %s", cmd)
		}
	}
}

func TestGuardInlineCommandsFailsClosed(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("NewExecTool() error: %v", err)
	}

	commands := []string{
		`echo "$(date"`,                          // unbalanced $(
		"echo `date",                             // unbalanced backtick
		`echo "${HOME"`,                          // unbalanced ${
		`echo "$(echo $(echo $(echo $(date))))"`, // deeper than the limit
		`echo "$(curl http://example.com/x)"`,    // network fetch inline
		`echo "$(wget http://example.com/x)"`,    // network fetch inline
	}

	for _, cmd := range commands {
		got := tool.guardCommand(cmd, t.TempDir())
		if got == "" {
			t.Errorf("unparseable or risky inline command must be blocked: %s", cmd)
		}
	}
}

func TestGuardInlineCommandsRespectCustomDeny(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	// Anchored pattern: it can only match the body of the inline command, not the
	// full text, so this test proves inline bodies really are checked.
	cfg.Tools.Exec.CustomDenyPatterns = []string{`^nasty\b`}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}

	got := tool.guardCommand(`echo "$(nasty thing)"`, t.TempDir())
	if !strings.Contains(got, "inline command") {
		t.Fatalf("custom deny patterns must apply inside inline commands, got: %q", got)
	}

	if got := tool.guardCommand(`echo "$(date)"`, t.TempDir()); got != "" {
		t.Fatalf("clean inline command should still pass, got: %q", got)
	}
}

func TestGuardInlineCommandsSkippedWhenDenyDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = false

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}

	if got := tool.guardCommand(`echo "$(date"`, t.TempDir()); got != "" {
		t.Fatalf("deny patterns disabled: inline check should not run, got: %q", got)
	}
}

// End-to-end: the command actually runs and returns the substituted value.
func TestShellTool_InlineSubstitutionRuns(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("NewExecTool() error: %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": `echo "value=$(pwd)"`,
	})
	if result.IsError {
		t.Fatalf("inline substitution should execute, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "value=") {
		t.Fatalf("unexpected output: %q", result.ForUser)
	}
}
