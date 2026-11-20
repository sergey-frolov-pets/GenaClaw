package pico

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestSend_CarriesUIObject(t *testing.T) {
	ch := newTestPicoChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "pico:sess-1",
		Content: "Задачи на сегодня: 7.",
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw: map[string]string{
				PayloadKeyUI: `{"v":1,"blockId":"oz-1","plugin":"ozadachnik.tasks"}`,
			},
		},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	msg := mustReceivePicoMessage(t, received)
	if msg.Type != TypeMessageCreate {
		t.Fatalf("type = %q, want %q", msg.Type, TypeMessageCreate)
	}
	ui, ok := msg.Payload[PayloadKeyUI].(map[string]any)
	if !ok {
		t.Fatalf("ui payload = %#v, want object", msg.Payload[PayloadKeyUI])
	}
	if got := ui["plugin"]; got != "ozadachnik.tasks" {
		t.Fatalf("ui.plugin = %#v", got)
	}
	if got := msg.Payload[PayloadKeyContent]; got != "Задачи на сегодня: 7." {
		t.Fatalf("content = %#v", got)
	}
}

func TestSend_InvalidUIIsIgnored(t *testing.T) {
	ch := newTestPicoChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "pico:sess-1",
		Content: "Текст без карточки.",
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw:     map[string]string{PayloadKeyUI: "{not json"},
		},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	msg := mustReceivePicoMessage(t, received)
	if _, ok := msg.Payload[PayloadKeyUI]; ok {
		t.Fatalf("broken ui reached payload: %#v", msg.Payload[PayloadKeyUI])
	}
	if got := msg.Payload[PayloadKeyContent]; got != "Текст без карточки." {
		t.Fatalf("content = %#v", got)
	}
}

func TestBeginStream_FinalUpdateCarriesUI(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{
		Enabled:         true,
		ThrottleSeconds: 1,
		MinGrowthChars:  1,
	}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if err := streamer.Update(context.Background(), "Задачи на сегодня: 7."); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	first := mustReceivePicoMessage(t, received)
	msgID, _ := first.Payload["message_id"].(string)

	uiSetter, ok := streamer.(bus.UIStreamer)
	if !ok {
		t.Fatalf("streamer does not implement bus.UIStreamer")
	}
	uiSetter.SetUIPayload([]byte(`{"v":1,"plugin":"ozadachnik.tasks"}`))

	// The final content equals the last streamed one: without the UI payload
	// the update would be skipped, so this also checks the forced edit.
	if err := streamer.Finalize(context.Background(), "Задачи на сегодня: 7."); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	final := mustReceivePicoMessage(t, received)
	if final.Type != TypeMessageUpdate {
		t.Fatalf("final type = %q, want %q", final.Type, TypeMessageUpdate)
	}
	if got := final.Payload["message_id"]; got != msgID {
		t.Fatalf("final message_id = %#v, want %q", got, msgID)
	}
	ui, ok := final.Payload[PayloadKeyUI].(map[string]any)
	if !ok {
		t.Fatalf("final ui payload = %#v, want object", final.Payload[PayloadKeyUI])
	}
	if got := ui["plugin"]; got != "ozadachnik.tasks" {
		t.Fatalf("final ui.plugin = %#v", got)
	}

	assertNoPicoMessage(t, received)
}
