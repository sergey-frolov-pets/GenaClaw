// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// errorNoticeCooldown prevents the same chat from being spammed with identical
// error notices when a provider keeps failing across consecutive turns.
const errorNoticeCooldown = 90 * time.Second

var lastErrorNotice sync.Map // chat key -> time.Time

// formatProcessingError builds a short, human-readable notice for the chat.
//
// Technical details (deadlines, HTTP bodies, stack traces) must never be
// published to the user: they end up as noise in a messenger. The raw error is
// logged separately by maybePublishError.
func formatProcessingError(err error) string {
	if err == nil {
		return ""
	}

	if kind, ok := providers.ClassifyAuthError(err); ok {
		return authErrorFriendlyMessage(kind)
	}
	if isTimeoutError(err) {
		return "⏳ Модель не ответила вовремя. Повтори сообщение — я переключусь на резервную модель."
	}
	if isRateLimitError(err) {
		return "⏳ Модель перегружена (лимит запросов). Повтори через минуту."
	}
	if isQuotaError(err) {
		return "⏳ У модели закончилась бесплатная квота на сегодня. Повтори позже или переключи модель."
	}
	return "⚠️ Не смог обработать сообщение. Технические детали записал в лог."
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "timed out")
}

func isRateLimitError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "too many requests")
}

func isQuotaError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "quota") ||
		strings.Contains(s, "insufficient balance") ||
		strings.Contains(s, "credit")
}

// shouldNotifyError reports whether a notice may be sent to this chat now.
// Duplicate notices inside errorNoticeCooldown are suppressed.
func shouldNotifyError(chatKey string) bool {
	now := time.Now()
	if v, ok := lastErrorNotice.Load(chatKey); ok {
		if ts, ok := v.(time.Time); ok && now.Sub(ts) < errorNoticeCooldown {
			return false
		}
	}
	lastErrorNotice.Store(chatKey, now)
	return true
}

func authErrorFriendlyMessage(kind providers.AuthErrorKind) string {
	switch kind {
	case providers.AuthErrorInvalidAPIKey:
		return "🔑 Ключ API не принят. Проверь ключ модели в настройках."
	case providers.AuthErrorMissingAPIKey:
		return "🔑 Для этой модели не задан ключ API. Добавь ключ в настройках моделей."
	case providers.AuthErrorExpiredToken:
		return "🔑 Токен входа истёк. Нужна повторная авторизация провайдера."
	default:
		return "🔑 Ошибка авторизации. Проверь ключ, токен или права доступа к модели."
	}
}
