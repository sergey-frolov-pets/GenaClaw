// Package uiblock extracts Gena UI blocks from assistant text.
//
// An assistant reply may end with a fenced JSON block:
//
//	Задачи на сегодня: 7, три просрочены.
//
//	```gena-ui
//	{"v":1,"plugin":"ozadachnik.tasks","data":{...},"actions":[...]}
//	```
//
// The block is a machine-readable payload for interactive cards in chat
// clients. It must never reach session history, model context, TTS or other
// channels, so the text is cleaned before it is stored: Extract removes every
// gena-ui fence and returns the last valid block separately.
//
// Invalid blocks are removed from the text as well (their JSON is useless to
// humans and expensive in context) and replaced with a short marker so the
// agent learns that the card did not arrive.
package uiblock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// FenceInfo is the code-fence info string that starts a UI block.
	FenceInfo = "gena-ui"

	// MaxBytes limits the raw JSON payload size.
	MaxBytes = 64 * 1024

	// MaxItems limits the number of items in a single list/board.
	MaxItems = 200

	// SchemaVersion is the payload schema version this package understands.
	SchemaVersion = 1

	// markerFormat is appended to the clean text when a block was dropped.
	markerFormat = "[ui-блок отброшен: %s]"
)

// Result is the outcome of Extract.
type Result struct {
	// Clean is the text without UI fences (with a marker if a block was dropped).
	Clean string
	// Block is the raw JSON of the last valid UI block, nil when absent.
	Block json.RawMessage
	// Plugin is the plugin id from the valid block, empty when absent.
	Plugin string
	// Dropped is the reason a found block was rejected, empty when not rejected.
	Dropped string
}

// HasBlock reports whether a valid UI block was found.
func (r Result) HasBlock() bool { return len(r.Block) > 0 }

// Extract splits text into human-readable content and an optional UI block.
//
// Every fence with the "gena-ui" info string is removed from the text. The last
// valid fence becomes the block. When the last fence is invalid, the block is
// empty, the reason is reported in Dropped and a marker is appended to Clean.
func Extract(text string) Result {
	if text == "" {
		return Result{}
	}
	if !strings.Contains(text, FenceInfo) {
		return Result{Clean: text}
	}

	segments, blocks := splitFences(text)
	clean := joinSegments(segments)

	result := Result{Clean: clean}
	if len(blocks) == 0 {
		return result
	}

	last := blocks[len(blocks)-1]
	if reason := Validate(last); reason != "" {
		result.Dropped = reason
		result.Clean = appendMarker(clean, reason)
		return result
	}

	result.Block = json.RawMessage(bytes.TrimSpace(last))
	result.Plugin = pluginOf(last)
	return result
}

// Validate returns an empty string when the block is acceptable, otherwise a
// short human-readable reason. The reason is shown to the agent, so it stays
// short and free of JSON details.
func Validate(block []byte) string {
	trimmed := bytes.TrimSpace(block)
	if len(trimmed) == 0 {
		return "пустой блок"
	}
	if len(trimmed) > MaxBytes {
		return fmt.Sprintf("больше %d КБ", MaxBytes/1024)
	}
	if trimmed[0] != '{' {
		return "не объект JSON"
	}

	var payload struct {
		V      int             `json:"v"`
		Plugin string          `json:"plugin"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return "невалидный JSON"
	}
	if payload.V != 0 && payload.V != SchemaVersion {
		return fmt.Sprintf("неизвестная версия схемы %d", payload.V)
	}
	if strings.TrimSpace(payload.Plugin) == "" {
		return "не указан plugin"
	}
	if len(payload.Plugin) > 200 {
		return "слишком длинное имя plugin"
	}
	if len(bytes.TrimSpace(payload.Data)) == 0 {
		return "нет данных (data)"
	}
	if count := countItems(payload.Data); count > MaxItems {
		return fmt.Sprintf("слишком много элементов (%d)", count)
	}
	return ""
}

// Plugin returns the plugin id of a valid block, or an empty string.
func Plugin(raw []byte) string {
	if Validate(raw) != "" {
		return ""
	}
	return pluginOf(raw)
}

func pluginOf(raw []byte) string {
	var payload struct {
		Plugin string `json:"plugin"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Plugin)
}

// countItems counts data.items plus every group's items.
func countItems(data json.RawMessage) int {
	var payload struct {
		Items  []json.RawMessage `json:"items"`
		Groups []struct {
			Items []json.RawMessage `json:"items"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	total := len(payload.Items)
	for _, group := range payload.Groups {
		total += len(group.Items)
	}
	return total
}

// splitFences walks text line by line and separates UI fences from the rest.
// It returns the remaining text segments and the raw bodies of the fences.
func splitFences(text string) (segments []string, blocks [][]byte) {
	lines := strings.Split(text, "\n")
	var current strings.Builder
	inFence := false
	var fence strings.Builder

	flushText := func() {
		if current.Len() == 0 {
			return
		}
		segments = append(segments, current.String())
		current.Reset()
	}
	flushFence := func() {
		// An empty body is still a block: it must be reported as dropped,
		// otherwise the fence disappears without a trace.
		blocks = append(blocks, []byte(strings.TrimSpace(fence.String())))
		fence.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if isFenceOpen(trimmed) {
				inFence = true
				flushText()
				continue
			}
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}
		if trimmed == "```" {
			inFence = false
			flushFence()
			continue
		}
		fence.WriteString(line)
		fence.WriteString("\n")
	}

	if inFence {
		// Unclosed fence: keep the tail as a block, the text is not lost.
		flushFence()
	} else {
		flushText()
	}
	if len(segments) == 0 {
		segments = append(segments, "")
	}
	return segments, blocks
}

func isFenceOpen(trimmedLine string) bool {
	if !strings.HasPrefix(trimmedLine, "```") {
		return false
	}
	info := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "```"))
	return info == FenceInfo
}

// joinSegments glues text segments back together and tidies up empty lines
// left where a fence used to be.
func joinSegments(segments []string) string {
	joined := strings.Join(segments, "")
	joined = strings.ReplaceAll(joined, "\r\n", "\n")
	for strings.Contains(joined, "\n\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n\n", "\n\n")
	}
	return strings.TrimRight(joined, " \t\n")
}

func appendMarker(clean, reason string) string {
	marker := fmt.Sprintf(markerFormat, reason)
	if clean == "" {
		return marker
	}
	return clean + "\n\n" + marker
}
