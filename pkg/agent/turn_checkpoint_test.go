// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

func testEvent(kind runtimeevents.Kind, sessionKey string, payload any) runtimeevents.Event {
	return runtimeevents.Event{
		Kind: kind,
		Scope: runtimeevents.Scope{
			SessionKey: sessionKey,
			Channel:    "pico",
			ChatID:     "chat-1",
			AgentID:    "main",
			TurnID:     "turn-1",
		},
		Payload: payload,
	}
}

func TestTurnCheckpointWritesPhaseAndTool(t *testing.T) {
	dir := t.TempDir()
	store := newTurnCheckpointStore(dir)
	if !store.Enabled() {
		t.Fatal("store should be enabled with workspace dir")
	}

	store.Note(testEvent(runtimeevents.KindAgentTurnStart, "pico:abc", TurnStartPayload{UserMessage: "привет"}))
	store.Note(testEvent(runtimeevents.KindAgentLLMRequest, "pico:abc", map[string]any{"iteration": 2}))
	store.Note(testEvent(runtimeevents.KindAgentToolExecStart, "pico:abc", map[string]any{
		"iteration": 2,
		"tool":      "exec",
	}))

	path := filepath.Join(dir, "state", "turns", safeSessionFile("pico:abc")+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("checkpoint file: %v", err)
	}
	var cp TurnCheckpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cp.Phase != "tools" {
		t.Errorf("phase = %q, want tools", cp.Phase)
	}
	if cp.LastTool != "exec" {
		t.Errorf("last_tool = %q, want exec", cp.LastTool)
	}
	if cp.Iteration != 2 {
		t.Errorf("iteration = %d, want 2", cp.Iteration)
	}
	if cp.Status != "running" {
		t.Errorf("status = %q, want running", cp.Status)
	}
	if cp.UserMessage != "привет" {
		t.Errorf("user_message = %q", cp.UserMessage)
	}
	if cp.Version != turnCheckpointVersion {
		t.Errorf("version = %d", cp.Version)
	}

	store.Note(testEvent(runtimeevents.KindAgentTurnEnd, "pico:abc", nil))
	raw, _ = os.ReadFile(path)
	if err := json.Unmarshal(raw, &cp); err != nil {
		t.Fatalf("unmarshal end: %v", err)
	}
	if cp.Status != "completed" {
		t.Errorf("status after end = %q", cp.Status)
	}
	store.Finish("pico:abc")
}

func TestTurnCheckpointStaleDetection(t *testing.T) {
	dir := t.TempDir()
	store := newTurnCheckpointStore(dir)
	base := time.Now()
	store.now = func() time.Time { return base }

	store.Note(testEvent(runtimeevents.KindAgentTurnStart, "pico:slow", TurnStartPayload{UserMessage: "долгая задача"}))
	store.Note(testEvent(runtimeevents.KindAgentLLMRequest, "pico:slow", map[string]any{"iteration": 3}))

	// Только что был шаг — не завис.
	if got := store.Stale(45 * time.Second); len(got) != 0 {
		t.Fatalf("fresh turn reported stale: %+v", got)
	}

	store.now = func() time.Time { return base.Add(60 * time.Second) }
	got := store.Stale(45 * time.Second)
	if len(got) != 1 {
		t.Fatalf("stale turns = %d, want 1", len(got))
	}
	if got[0].SessionKey != "pico:slow" || got[0].Phase != "llm" || got[0].StalledForS < 59 {
		t.Errorf("unexpected stale record: %+v", got[0])
	}
	// Повторно тот же ход не должен считаться running.
	if got := store.Stale(45 * time.Second); len(got) != 0 {
		t.Errorf("stalled turn reported twice: %+v", got)
	}
}

func TestTurnCheckpointPhaseThresholds(t *testing.T) {
	dir := t.TempDir()
	store := newTurnCheckpointStore(dir)
	base := time.Now()
	store.now = func() time.Time { return base }

	// Долгий exec законно молчит: 60 с тишины — не зависание.
	store.Note(testEvent(runtimeevents.KindAgentTurnStart, "pico:exec", nil))
	store.Note(testEvent(runtimeevents.KindAgentToolExecStart, "pico:exec", map[string]any{"tool": "exec"}))
	store.now = func() time.Time { return base.Add(60 * time.Second) }
	if got := store.Stale(45 * time.Second); len(got) != 0 {
		t.Errorf("exec within 3m reported stale: %+v", got)
	}
	store.now = func() time.Time { return base.Add(200 * time.Second) }
	if got := store.Stale(45 * time.Second); len(got) != 1 {
		t.Errorf("exec over 3m must be stale, got %+v", got)
	}

	// Фоновая суммаризация не считается зависшим ходом.
	store.Note(testEvent(runtimeevents.KindAgentSessionSummarize, "pico:sum", nil))
	store.now = func() time.Time { return base.Add(3600 * time.Second) }
	if got := store.Stale(45 * time.Second); len(got) != 0 {
		t.Errorf("summarize reported stale: %+v", got)
	}
}

func TestTurnCheckpointDisabledWithoutWorkspace(t *testing.T) {
	store := newTurnCheckpointStore("")
	if store.Enabled() {
		t.Fatal("store without workspace must be disabled")
	}
	store.Note(testEvent(runtimeevents.KindAgentTurnStart, "pico:x", nil))
	if got := store.Stale(time.Second); got != nil {
		t.Errorf("disabled store returned stale turns: %+v", got)
	}
}

func TestSafeSessionFile(t *testing.T) {
	cases := map[string]string{
		"":                    "unknown",
		"pico:sess-1":         "pico_sess-1",
		"telegram/1234":       "telegram_1234",
		"../etc/passwd":       "__etc_passwd",
	}
	for in, want := range cases {
		if got := safeSessionFile(in); got != want {
			t.Errorf("safeSessionFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTurnCheckpointPruneKeepsFreshFiles(t *testing.T) {
	dir := t.TempDir()
	store := newTurnCheckpointStore(dir)
	store.keepDays = 1

	oldPath := filepath.Join(dir, "state", "turns", "old.json")
	freshPath := filepath.Join(dir, "state", "turns", "fresh.json")
	for _, p := range []string{oldPath, freshPath} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().AddDate(0, 0, -10)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}

	if removed := store.Prune(); removed != 1 {
		t.Fatalf("pruned %d files, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old checkpoint should be removed")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Error("fresh checkpoint must survive")
	}
}

func TestTurnCheckpointEnvSwitch(t *testing.T) {
	t.Setenv(envTurnCheckpoint, "off")
	store := newTurnCheckpointStore(t.TempDir())
	if store.Enabled() {
		t.Fatal("store must be disabled when env switch is off")
	}
	store.Note(testEvent(runtimeevents.KindAgentTurnStart, "pico:off", nil))
	if got := store.Stale(time.Second); got != nil {
		t.Errorf("disabled store collected turns: %+v", got)
	}
}
