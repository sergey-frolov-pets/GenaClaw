// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"encoding/json"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/uiblock"
)

// uiRawKey is the outbound Context.Raw key that carries a card block to the
// channel. It travels as a JSON string because Raw is map[string]string; the
// pico channel unmarshals it back into an object before sending.
const uiRawKey = "ui"

// extractUI splits a gena-ui card block off assistant text.
//
// The returned text is safe for history, TTS, Telegram/VK and model context.
// The block is nil when the text has no valid fence; a rejected fence leaves a
// short marker in the text so the agent notices the card did not arrive.
func extractUI(text string) (string, json.RawMessage) {
	res := uiblock.Extract(text)
	if res.Dropped != "" {
		logger.WarnCF("agent", "gena-ui block dropped", map[string]any{
			"reason": res.Dropped,
			"plugin": res.Plugin,
		})
	}
	if !res.HasBlock() {
		return res.Clean, nil
	}
	return res.Clean, res.Block
}

// rawWithUI stores the card block in an outbound Raw map. Returns the input map
// unchanged when there is nothing to add.
func rawWithUI(raw map[string]string, ui json.RawMessage) map[string]string {
	if len(ui) == 0 {
		return raw
	}
	if raw == nil {
		raw = make(map[string]string, 1)
	}
	raw[uiRawKey] = string(ui)
	return raw
}
