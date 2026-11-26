// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"os"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// =============================================================================
// turn_id в записях сессии (этап C плана памяти)
// =============================================================================
//
// Зачем. Чекпойнт хода пишет, «где застрял ход», но не отвечает на вопрос
// «какие сообщения этого хода». По turn_id из state/turns/*.json можно
// джойнить снимок с записями sessions/*.jsonl и разбирать обрыв пост-фактум.
//
// Как. Декоратор над session.SessionStore: пока ход идёт, каждое сообщение
// получает TurnID. Ни формат сессии, ни вызовы AddMessage/AddFullMessage не
// меняются; старые записи читаются как раньше (turn_id отсутствует).
//
// Рубильник: PICOCLAW_TURN_STAMP=off — не штамповать.

const envTurnStamp = "PICOCLAW_TURN_STAMP"

func turnStampEnabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envTurnStamp))) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	}
	return true
}

type turnStampStore struct {
	inner   session.SessionStore
	enabled bool

	mu      sync.RWMutex
	current map[string]string // sessionKey -> turnID текущего хода
}

func newTurnStampStore(inner session.SessionStore) *turnStampStore {
	if inner == nil {
		return nil
	}
	return &turnStampStore{
		inner:   inner,
		enabled: turnStampEnabledByEnv(),
		current: make(map[string]string),
	}
}

// SetTurn запоминает активный ход сессии.
func (s *turnStampStore) SetTurn(sessionKey, turnID string) {
	if s == nil || !s.enabled {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	turnID = strings.TrimSpace(turnID)
	if sessionKey == "" {
		return
	}
	s.mu.Lock()
	if turnID == "" {
		delete(s.current, sessionKey)
	} else {
		s.current[sessionKey] = turnID
	}
	s.mu.Unlock()
}

// ClearTurn забывает активный ход сессии.
func (s *turnStampStore) ClearTurn(sessionKey string) {
	s.SetTurn(sessionKey, "")
}

// turnStampMetaStore — вариант штампа для стора, который умеет метаданные
// сессий. Обёртка обязана сохранять возможности внутреннего стора: маршрутизация
// агента (session.ResolveAgentID) и alias-метаданные ищут интерфейс
// session.MetadataAwareSessionStore через type assertion на agent.Sessions.
type turnStampMetaStore struct {
	*turnStampStore
	meta session.MetadataAwareSessionStore
}

func (s *turnStampMetaStore) EnsureSessionMetadata(sessionKey string, scope *session.SessionScope, aliases []string) {
	s.meta.EnsureSessionMetadata(sessionKey, scope, aliases)
}

func (s *turnStampMetaStore) ResolveSessionKey(sessionKey string) string {
	return s.meta.ResolveSessionKey(sessionKey)
}

func (s *turnStampMetaStore) GetSessionScope(sessionKey string) *session.SessionScope {
	return s.meta.GetSessionScope(sessionKey)
}

// turnSetter / turnClearer — минимальные возможности штампа. Через интерфейс,
// а не через *turnStampStore: стор может быть обёрнут в turnStampMetaStore.
type turnSetter interface {
	SetTurn(sessionKey, turnID string)
}

type turnClearer interface {
	ClearTurn(sessionKey string)
}

// wrapWithTurnStamp оборачивает стор штампом turn_id, сохраняя поддержку
// метаданных, если она есть у внутреннего стора.
func wrapWithTurnStamp(inner session.SessionStore) session.SessionStore {
	stamps := newTurnStampStore(inner)
	if stamps == nil {
		return inner
	}
	if meta, ok := inner.(session.MetadataAwareSessionStore); ok {
		return &turnStampMetaStore{turnStampStore: stamps, meta: meta}
	}
	return stamps
}

// clearTurnStamp снимает штамп по завершении хода: сообщения вне хода
// (сжатие, служебные апдейты) не должны получать turn_id завершённого хода.
func (ts *turnState) clearTurnStamp() {
	if ts == nil || ts.session == nil {
		return
	}
	if st, ok := ts.session.(turnClearer); ok {
		st.ClearTurn(ts.sessionKey)
	}
}


func (s *turnStampStore) turnFor(sessionKey string) string {
	if s == nil || !s.enabled {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current[strings.TrimSpace(sessionKey)]
}

func (s *turnStampStore) AddMessage(sessionKey, role, content string) {
	if id := s.turnFor(sessionKey); id != "" {
		s.inner.AddFullMessage(sessionKey, providers.Message{
			Role:    role,
			Content: content,
			TurnID:  id,
		})
		return
	}
	s.inner.AddMessage(sessionKey, role, content)
}

func (s *turnStampStore) AddFullMessage(sessionKey string, msg providers.Message) {
	if msg.TurnID == "" {
		msg.TurnID = s.turnFor(sessionKey)
	}
	s.inner.AddFullMessage(sessionKey, msg)
}

func (s *turnStampStore) GetHistory(key string) []providers.Message {
	return s.inner.GetHistory(key)
}

func (s *turnStampStore) GetSummary(key string) string {
	return s.inner.GetSummary(key)
}

func (s *turnStampStore) SetSummary(key, summary string) {
	s.inner.SetSummary(key, summary)
}

func (s *turnStampStore) SetHistory(key string, history []providers.Message) {
	s.inner.SetHistory(key, history)
}

func (s *turnStampStore) TruncateHistory(key string, keepLast int) {
	s.inner.TruncateHistory(key, keepLast)
}

func (s *turnStampStore) Save(key string) error {
	return s.inner.Save(key)
}

func (s *turnStampStore) ListSessions() []string {
	return s.inner.ListSessions()
}

func (s *turnStampStore) Close() error {
	return s.inner.Close()
}
