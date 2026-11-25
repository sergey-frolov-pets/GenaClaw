// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// =============================================================================
// Checkpoint хода (идея из LangGraph: состояние после каждого узла)
// =============================================================================
//
// Зачем. Сессия `.jsonl` фиксирует сообщения, но не «где именно застрял ход».
// 19.11.2026 ход молчал 10 минут на 100 итерациях, и по логам нельзя было
// сказать, ждём ли мы провайдера, крутим ли инструмент или упали в
// reasoning-only. Чекпойнт пишет маленький файл на каждый шаг хода:
// фаза, итерация, последний инструмент, время последнего шага.
//
// Он не меняет исполнение: только пишет файл и считает «тишину». Чтение
// (resume после обрыва) — следующий этап, поэтому формат сразу версионирован.
//
// Файл: <workspace>/state/turns/<session>.json (перезапись атомарным rename,
// без изменений в sessions/*.jsonl).

const turnCheckpointVersion = 1

// TurnCheckpoint — снимок текущего хода.
type TurnCheckpoint struct {
	Version     int       `json:"version"`
	SessionKey  string    `json:"session_key"`
	Channel     string    `json:"channel,omitempty"`
	ChatID      string    `json:"chat_id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	TurnID      string    `json:"turn_id,omitempty"`
	Phase       string    `json:"phase"`
	Iteration   int       `json:"iteration"`
	Step        int       `json:"step"`
	LastTool    string    `json:"last_tool,omitempty"`
	UserMessage string    `json:"user_message,omitempty"`
	Status      string    `json:"status"` // running|stalled|completed|aborted|error
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	StalledForS float64   `json:"stalled_for_s,omitempty"`
}

// TurnCheckpointStore пишет чекпойнты и ищет зависшие ходы.
type TurnCheckpointStore struct {
	dir      string
	enabled  bool
	keepDays int

	mu       sync.Mutex
	turns    map[string]*TurnCheckpoint // sessionKey -> state
	lastBeat map[string]time.Time
	now      func() time.Time
}

// Рубильник и сроки — через окружение, чтобы отключить чекпойнт без пересборки.
const (
	envTurnCheckpoint         = "PICOCLAW_TURN_CHECKPOINT"          // off|0|false — выключить
	envTurnCheckpointKeepDays = "PICOCLAW_TURN_CHECKPOINT_KEEP_DAYS" // дней хранить (по умолчанию 7)
)

func envInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func checkpointEnabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envTurnCheckpoint))) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	}
	return true
}

func newTurnCheckpointStore(workspace string) *TurnCheckpointStore {
	dir := ""
	if strings.TrimSpace(workspace) != "" {
		dir = filepath.Join(workspace, "state", "turns")
	}
	s := &TurnCheckpointStore{
		dir:      dir,
		enabled:  dir != "" && checkpointEnabledByEnv(),
		keepDays: envInt(envTurnCheckpointKeepDays, 7),
		turns:    make(map[string]*TurnCheckpoint),
		lastBeat: make(map[string]time.Time),
		now:      time.Now,
	}
	if dir != "" && !s.enabled {
		logger.InfoCF("agent", "turn checkpoints disabled by env", map[string]any{"env": envTurnCheckpoint})
	}
	if s.enabled {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.WarnCF("agent", "checkpoint dir create failed", map[string]any{"error": err.Error()})
			s.enabled = false
		}
	}
	return s
}

// Enabled сообщает, есть ли куда писать.
func (s *TurnCheckpointStore) Enabled() bool { return s != nil && s.enabled }

// Prune удаляет чекпойнты старше keepDays, чтобы каталог не рос бесконечно.
func (s *TurnCheckpointStore) Prune() int {
	if !s.Enabled() || s.keepDays <= 0 {
		return 0
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}
	cutoff := s.now().AddDate(0, 0, -s.keepDays)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, e.Name())); err == nil {
			removed++
		}
	}
	return removed
}

func safeSessionFile(sessionKey string) string {
	if strings.TrimSpace(sessionKey) == "" {
		return "unknown"
	}
	repl := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", "..", "_")
	name := repl.Replace(sessionKey)
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

// phaseForEvent переводит событие хода в фазу чекпойнта.
// Неизвестные события фазу не меняют ("").
func phaseForEvent(kind runtimeevents.Kind) string {
	switch kind {
	case runtimeevents.KindAgentTurnStart:
		return "start"
	case runtimeevents.KindAgentLLMRequest:
		return "llm"
	case runtimeevents.KindAgentLLMResponse:
		return "llm_done"
	case runtimeevents.KindAgentToolExecStart:
		return "tools"
	case runtimeevents.KindAgentToolExecEnd:
		return "tools_done"
	case runtimeevents.KindAgentToolExecSkipped:
		return "tools_skipped"
	case runtimeevents.KindAgentContextCompress:
		return "compact"
	case runtimeevents.KindAgentSessionSummarize:
		return "summarize"
	case runtimeevents.KindAgentTurnEnd:
		return "end"
	case runtimeevents.KindAgentError:
		return "error"
	}
	return ""
}

// Note фиксирует шаг хода. Вызывается из emitEvent, поэтому обязан быть дешёвым:
// один map-lookup под мьютексом и один небольшой write, без сети и ожиданий.
func (s *TurnCheckpointStore) Note(evt runtimeevents.Event) {
	if !s.Enabled() {
		return
	}
	phase := phaseForEvent(evt.Kind)
	if phase == "" {
		return
	}

	now := s.now()
	sessionKey := strings.TrimSpace(evt.Scope.SessionKey)
	if sessionKey == "" {
		return
	}
	userMessage := userMessageFromPayload(evt.Payload)

	s.mu.Lock()
	cp := s.turns[sessionKey]
	if cp == nil || phase == "start" {
		cp = &TurnCheckpoint{
			Version:    turnCheckpointVersion,
			SessionKey: sessionKey,
			StartedAt:  now,
			Status:     "running",
		}
		if trimmed := strings.TrimSpace(userMessage); trimmed != "" {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "…"
			}
			cp.UserMessage = trimmed
		}
		s.turns[sessionKey] = cp
	}
	if evt.Scope.Channel != "" {
		cp.Channel = evt.Scope.Channel
	}
	if evt.Scope.ChatID != "" {
		cp.ChatID = evt.Scope.ChatID
	}
	if evt.Scope.AgentID != "" {
		cp.AgentID = evt.Scope.AgentID
	}
	if evt.Scope.TurnID != "" {
		cp.TurnID = evt.Scope.TurnID
	}
	if phase != "start" {
		cp.Phase = phase
	}
	cp.Step++
	cp.UpdatedAt = now
	cp.Status = "running"
	cp.StalledForS = 0
	if name := toolNameFromPayload(evt.Payload); name != "" {
		cp.LastTool = name
	}
	if evt.Scope.Iteration > 0 {
		cp.Iteration = evt.Scope.Iteration
	} else if it := iterationFromPayload(evt.Payload); it > 0 {
		cp.Iteration = it
	}
	if phase == "end" {
		cp.Status = "completed"
		cp.Phase = "end"
	}
	if phase == "error" {
		cp.Status = "error"
	}
	s.lastBeat[sessionKey] = now
	snapshot := *cp
	s.mu.Unlock()

	s.write(sessionKey, snapshot)
}

// toolNameFromPayload достаёт имя инструмента из payload события.
func toolNameFromPayload(payload any) string {
	switch p := payload.(type) {
	case ToolExecStartPayload:
		return strings.TrimSpace(p.Tool)
	case ToolExecEndPayload:
		return strings.TrimSpace(p.Tool)
	case ToolExecSkippedPayload:
		return strings.TrimSpace(p.Tool)
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"tool", "tool_name", "name"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// userMessageFromPayload достаёт начало сообщения пользователя (turn.start).
func userMessageFromPayload(payload any) string {
	switch p := payload.(type) {
	case map[string]any:
		if v, ok := p["user_message"].(string); ok {
			return v
		}
	case TurnStartPayload:
		return p.UserMessage
	}
	return ""
}

func iterationFromPayload(payload any) int {
	m, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	switch v := m["iteration"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

func (s *TurnCheckpointStore) write(sessionKey string, cp TurnCheckpoint) {
	if !s.Enabled() {
		return
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, safeSessionFile(sessionKey)+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		logger.WarnCF("agent", "checkpoint write failed", map[string]any{"error": err.Error()})
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logger.WarnCF("agent", "checkpoint rename failed", map[string]any{"error": err.Error()})
		return
	}
	s.appendJournal(sessionKey, cp)
}

// appendJournal дописывает шаг хода в журнал <session>-<turn>.jsonl.
// Снимок .json отвечает «где сейчас», журнал — «как сюда шли» (пост-мортем
// после тишины или обрыва). Файл журнала пересоздаётся на новом ходе.
func (s *TurnCheckpointStore) appendJournal(sessionKey string, cp TurnCheckpoint) {
	turnID := cp.TurnID
	if turnID == "" {
		turnID = "turn"
	}
	journalPath := filepath.Join(s.dir, safeSessionFile(sessionKey)+"-"+safeSessionFile(turnID)+".jsonl")

	line, err := json.Marshal(struct {
		At        time.Time `json:"at"`
		Phase     string    `json:"phase"`
		Iteration int       `json:"iteration,omitempty"`
		Step      int       `json:"step"`
		LastTool  string    `json:"last_tool,omitempty"`
		Status    string    `json:"status"`
	}{cp.UpdatedAt, cp.Phase, cp.Iteration, cp.Step, cp.LastTool, cp.Status})
	if err != nil {
		return
	}
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if cp.Phase == "start" {
		flags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	f, err := os.OpenFile(journalPath, flags, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// stallThreshold — порог тишины для фазы. Разные узлы живут разное время:
// summarize идёт в фоне и к ходу пользователя не относится, а инструмент
// (exec, длинная команда) законно молчит минутами.
func stallThreshold(base time.Duration, cp *TurnCheckpoint) time.Duration {
	switch cp.Phase {
	case "summarize", "end":
		return 0
	case "tools":
		if cp.LastTool == "" || cp.LastTool == "exec" {
			if base < 3*time.Minute {
				return 3 * time.Minute
			}
			return base
		}
		if base < 90*time.Second {
			return 90 * time.Second
		}
	}
	return base
}

// Stale возвращает ходы, которые числятся running, но не подавали признаков
// жизни дольше порога. Используется watchdog-горутиной и тестами.
func (s *TurnCheckpointStore) Stale(stallAfter time.Duration) []TurnCheckpoint {
	if !s.Enabled() || stallAfter <= 0 {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TurnCheckpoint
	for key, cp := range s.turns {
		if cp.Status != "running" {
			continue
		}
		beat := s.lastBeat[key]
		if beat.IsZero() {
			beat = cp.UpdatedAt
		}
		thr := stallThreshold(stallAfter, cp)
		if thr <= 0 || now.Sub(beat) < thr {
			continue
		}
		cp.Status = "stalled"
		cp.StalledForS = now.Sub(beat).Seconds()
		out = append(out, *cp)
	}
	return out
}

// Finish помечает ход завершённым (вызывается на turn.end).
func (s *TurnCheckpointStore) Finish(sessionKey string) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	if cp, ok := s.turns[sessionKey]; ok && cp.Status == "running" {
		cp.Status = "completed"
		cp.UpdatedAt = s.now()
	}
	delete(s.turns, sessionKey)
	delete(s.lastBeat, sessionKey)
	s.mu.Unlock()
}

// Watchdog каждые interval проверяет признаки жизни у running-ходов и пишет
// предупреждение + событие KindAgentTurnStalled. В чат ничего не отправляет:
// v1 — только наблюдаемость, user-facing подсказка — следующий этап.
func (s *TurnCheckpointStore) Watchdog(ctx context.Context, interval, stallAfter time.Duration,
	emit func(TurnCheckpoint)) {
	if !s.Enabled() || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	notified := make(map[string]time.Time)
	if removed := s.Prune(); removed > 0 {
		logger.InfoCF("agent", "checkpoint pruned", map[string]any{"files": removed, "keep_days": s.keepDays})
	}
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ticks++
			if ticks%40 == 0 { // раз в ~10 минут при тике 15 с
				s.Prune()
			}
			for _, cp := range s.Stale(stallAfter) {
				if last, ok := notified[cp.SessionKey]; ok && time.Since(last) < stallAfter {
					continue
				}
				notified[cp.SessionKey] = time.Now()
				logger.WarnCF("agent", "turn stalled: no progress", map[string]any{
					"session":       cp.SessionKey,
					"phase":         cp.Phase,
					"iteration":     cp.Iteration,
					"step":          cp.Step,
					"last_tool":     cp.LastTool,
					"stalled_for_s": cp.StalledForS,
				})
				if emit != nil {
					emit(cp)
				}
			}
		}
	}
}

// StalledPayload — payload события KindAgentTurnStalled.
type StalledPayload struct {
	SessionKey  string  `json:"session_key"`
	Phase       string  `json:"phase"`
	Iteration   int     `json:"iteration"`
	Step        int     `json:"step"`
	LastTool    string  `json:"last_tool,omitempty"`
	StalledForS float64 `json:"stalled_for_s"`
	UserMessage string  `json:"user_message,omitempty"`
}
