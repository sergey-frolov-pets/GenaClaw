package agent

import (
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// Long tool outputs are the main reason a long session blows past the context
// budget: a single `read_file` can add tens of kilobytes to the history, and
// nothing trims it because summarization only runs near the context window.
// shrinkToolResults fixes the history that is actually sent to the model:
//
//   - results older than the last keepRecent messages are replaced with a
//     one-line marker plus a short preview (the agent can re-read the source);
//   - the newest results are capped at maxBytes, keeping head and tail.
//
// The session file on disk is not modified, so nothing is lost.
func shrinkToolResults(history []providers.Message, maxBytes, keepRecent int) []providers.Message {
	if len(history) == 0 || (maxBytes <= 0 && keepRecent <= 0) {
		return history
	}

	out := make([]providers.Message, len(history))
	copy(out, history)

	cutoff := len(history) - keepRecent

	for i := range out {
		msg := &out[i]
		if msg.Role != "tool" || msg.Content == "" {
			continue
		}

		size := len(msg.Content)
		isOld := keepRecent > 0 && i < cutoff

		if isOld {
			msg.Content = fmt.Sprintf(
				"[tool result trimmed: %d bytes from an earlier turn; re-read the file or rerun the command if the details are needed] %s",
				size, firstRunes(msg.Content, 200),
			)
			continue
		}

		if maxBytes > 0 && size > maxBytes {
			msg.Content = truncateMiddleBytes(msg.Content, maxBytes)
		}
	}

	return out
}

func firstRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// truncateMiddleBytes keeps the head and the tail of s so that both the
// beginning of a file and its last lines (often the useful part) survive.
func truncateMiddleBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	head := maxBytes * 2 / 3
	tail := maxBytes - head
	if tail < 0 {
		tail = 0
	}
	// Cut on rune boundaries so UTF-8 stays valid.
	headStr := firstRunesValid(s, head)
	tailStr := lastRunesValid(s, tail)
	removed := len(s) - len(headStr) - len(tailStr)
	return fmt.Sprintf("%s\n\n[... %d bytes trimmed; full output is in the session file, use read_file with offset/length ...]\n\n%s",
		headStr, removed, tailStr)
}

func firstRunesValid(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func lastRunesValid(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !isRuneStart(s[start]) {
		start++
	}
	return s[start:]
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// hasToolResults reports whether history contains at least one tool message.
func hasToolResults(history []providers.Message) bool {
	for i := range history {
		if history[i].Role == "tool" && strings.TrimSpace(history[i].Content) != "" {
			return true
		}
	}
	return false
}
