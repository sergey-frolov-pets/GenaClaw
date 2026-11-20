// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testUIBlock = `{"v":1,"blockId":"oz-1","plugin":"ozadachnik.tasks","data":{"view":"list","items":[{"id":"a1","title":"План"}]}}`

func fenceUI(body string) string {
	return "```gena-ui\n" + body + "\n```"
}

func TestExtractUICutsBlockFromText(t *testing.T) {
	clean, ui := extractUI("Задачи на сегодня: 7.\n\n" + fenceUI(testUIBlock))
	if clean != "Задачи на сегодня: 7." {
		t.Fatalf("clean = %q", clean)
	}
	if len(ui) == 0 {
		t.Fatal("block not extracted")
	}
	var payload map[string]any
	if err := json.Unmarshal(ui, &payload); err != nil {
		t.Fatalf("block is not valid JSON: %v", err)
	}
	if got := payload["plugin"]; got != "ozadachnik.tasks" {
		t.Fatalf("plugin = %#v", got)
	}
}

func TestExtractUILeavesMarkerForBrokenBlock(t *testing.T) {
	clean, ui := extractUI("Резюме.\n\n" + fenceUI(`{"v":1,"plugin":`))
	if len(ui) != 0 {
		t.Fatalf("broken block accepted: %s", ui)
	}
	if strings.Contains(clean, "```gena-ui") || strings.Contains(clean, `"plugin"`) {
		t.Fatalf("fence left in text: %q", clean)
	}
	if !strings.Contains(clean, "[ui-блок отброшен:") {
		t.Fatalf("marker missing: %q", clean)
	}
}

func TestExtractUIPlainTextUnchanged(t *testing.T) {
	text := "Обычный ответ."
	clean, ui := extractUI(text)
	if clean != text || len(ui) != 0 {
		t.Fatalf("plain text changed: %q / %s", clean, ui)
	}
}

func TestRawWithUI(t *testing.T) {
	raw := rawWithUI(nil, json.RawMessage(testUIBlock))
	if got := raw[uiRawKey]; got != testUIBlock {
		t.Fatalf("ui raw = %q", got)
	}

	existing := map[string]string{"model_name": "ds-pro"}
	merged := rawWithUI(existing, json.RawMessage(testUIBlock))
	if merged["model_name"] != "ds-pro" {
		t.Fatalf("existing key lost: %#v", merged)
	}
	if merged[uiRawKey] != testUIBlock {
		t.Fatalf("ui not merged: %#v", merged)
	}

	if got := rawWithUI(map[string]string{"model_name": "ds-pro"}, nil); got[uiRawKey] != "" {
		t.Fatalf("nil ui added a key: %#v", got)
	}
}

func TestPublishResponseIfNeededExtractsCard(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	al.PublishResponseIfNeeded(
		context.Background(),
		"pico",
		"pico:session-1",
		"session-1",
		"Задачи на сегодня: 7.\n\n"+fenceUI(testUIBlock),
	)

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "Задачи на сегодня: 7." {
			t.Fatalf("outbound content = %q", outbound.Content)
		}
		if got := outbound.Context.Raw[uiRawKey]; got != testUIBlock {
			t.Fatalf("outbound ui = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected outbound")
	}
}
