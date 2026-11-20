package channels

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestOutboundMessageEditPayloadCarriesUIObject(t *testing.T) {
	msg := bus.OutboundMessage{
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw: map[string]string{
				"model_name": "ds-pro",
				"ui":         `{"v":1,"blockId":"oz-1","plugin":"ozadachnik.tasks"}`,
			},
		},
	}

	payload := outboundMessageEditPayload(msg, "Задачи на сегодня: 7.")
	if got := payload["content"]; got != "Задачи на сегодня: 7." {
		t.Fatalf("content = %#v", got)
	}
	if got := payload["model_name"]; got != "ds-pro" {
		t.Fatalf("model_name = %#v", got)
	}
	ui, ok := payload["ui"].(map[string]any)
	if !ok {
		t.Fatalf("ui = %#v, want object", payload["ui"])
	}
	if got := ui["plugin"]; got != "ozadachnik.tasks" {
		t.Fatalf("ui.plugin = %#v", got)
	}
}

func TestOutboundMessageEditPayloadSkipsBrokenUI(t *testing.T) {
	msg := bus.OutboundMessage{
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw:     map[string]string{"ui": "{not json"},
		},
	}

	payload := outboundMessageEditPayload(msg, "Текст.")
	if _, ok := payload["ui"]; ok {
		t.Fatalf("broken ui reached payload: %#v", payload["ui"])
	}
	if got := payload["content"]; got != "Текст." {
		t.Fatalf("content = %#v", got)
	}
}
