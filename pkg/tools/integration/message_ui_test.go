package integrationtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

const messageUIBlock = `{"v":1,"blockId":"oz-1","plugin":"ozadachnik.tasks","data":{"view":"list"}}`

func TestMessageTool_CardBlockGoesToCardCallback(t *testing.T) {
	tool := NewMessageTool()

	var sentContent string
	var sentUI json.RawMessage
	tool.SetSendCallback(func(
		ctx context.Context,
		channel, chatID, content, replyToMessageID string,
		mediaParts []bus.MediaPart,
	) error {
		t.Fatalf("plain callback used for a card message")
		return nil
	})
	tool.SetSendCardCallback(func(
		ctx context.Context,
		channel, chatID, content, replyToMessageID string,
		mediaParts []bus.MediaPart,
		ui json.RawMessage,
	) error {
		sentContent = content
		sentUI = ui
		return nil
	})

	ctx := WithToolContext(context.Background(), "pico", "pico:sess-1")
	result := tool.Execute(ctx, map[string]any{
		"content": "Задачи на сегодня: 7.\n\n```gena-ui\n" + messageUIBlock + "\n```",
	})

	if result == nil || result.IsError {
		t.Fatalf("execute failed: %+v", result)
	}
	if sentContent != "Задачи на сегодня: 7." {
		t.Fatalf("content = %q", sentContent)
	}
	if strings.Contains(sentContent, "gena-ui") || strings.Contains(sentContent, `"plugin"`) {
		t.Fatalf("block left in content: %q", sentContent)
	}
	var payload map[string]any
	if err := json.Unmarshal(sentUI, &payload); err != nil {
		t.Fatalf("ui is not valid JSON: %v", err)
	}
	if got := payload["plugin"]; got != "ozadachnik.tasks" {
		t.Fatalf("ui.plugin = %#v", got)
	}
}

func TestMessageTool_BlockIsCutEvenWithoutCardCallback(t *testing.T) {
	tool := NewMessageTool()

	var sentContent string
	tool.SetSendCallback(func(
		ctx context.Context,
		channel, chatID, content, replyToMessageID string,
		mediaParts []bus.MediaPart,
	) error {
		sentContent = content
		return nil
	})

	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"content": "Резюме.\n\n```gena-ui\n" + messageUIBlock + "\n```",
	})

	if result == nil || result.IsError {
		t.Fatalf("execute failed: %+v", result)
	}
	if sentContent != "Резюме." {
		t.Fatalf("content = %q, want clean text", sentContent)
	}
}
