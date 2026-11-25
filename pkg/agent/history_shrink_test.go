package agent

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestShrinkToolResultsTrimsOldResults(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "прочитай файл"},
		{Role: "tool", Content: strings.Repeat("a", 5000)},
		{Role: "assistant", Content: "ок"},
		{Role: "user", Content: "ещё раз"},
		{Role: "tool", Content: strings.Repeat("b", 5000)},
	}

	out := shrinkToolResults(history, 0, 2)

	if !strings.Contains(out[1].Content, "trimmed") {
		t.Fatalf("old tool result must be trimmed, got %q", out[1].Content[:60])
	}
	if len(out[1].Content) >= 1000 {
		t.Fatalf("old tool result still too long: %d", len(out[1].Content))
	}
	if out[4].Content != history[4].Content {
		t.Fatalf("recent tool result must be kept intact")
	}
	if out[0].Content != history[0].Content {
		t.Fatalf("non-tool messages must be untouched")
	}
}

func TestShrinkToolResultsCapsRecentResult(t *testing.T) {
	body := strings.Repeat("x", 20000)
	history := []providers.Message{{Role: "tool", Content: body}}

	out := shrinkToolResults(history, 8000, 6)
	got := out[0].Content

	if len(got) > 9500 {
		t.Fatalf("result not capped: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "xxx") {
		t.Fatalf("head must be kept")
	}
	if !strings.HasSuffix(got, "xxx") {
		t.Fatalf("tail must be kept")
	}
	if !strings.Contains(got, "bytes trimmed") {
		t.Fatalf("trim marker missing")
	}
}

func TestShrinkToolResultsKeepsUtf8(t *testing.T) {
	body := strings.Repeat("привет ", 5000)
	out := shrinkToolResults([]providers.Message{{Role: "tool", Content: body}}, 4000, 6)
	if strings.ContainsRune(out[0].Content, '\uFFFD') {
		t.Fatalf("truncation broke UTF-8")
	}
}

func TestShrinkToolResultsDisabled(t *testing.T) {
	history := []providers.Message{{Role: "tool", Content: strings.Repeat("z", 100)}}
	out := shrinkToolResults(history, 0, 0)
	if out[0].Content != history[0].Content {
		t.Fatalf("with both limits zero nothing must change")
	}
}
