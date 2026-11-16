package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers/common"
)

// Технические детали не должны попадать в мессенджер ни при каком типе ошибки.
func TestFormatProcessingError_NoTechnicalLeak(t *testing.T) {
	cases := []error{
		errors.New("LLM call failed after retries: failed to inspect response: context deadline exceeded (Client.Timeout or context cancellation while reading body)"),
		fmt.Errorf("wrap: %w", context.DeadlineExceeded),
		errors.New(`API request failed: Status: 429 Body: {"error":{"message":"rate limit reached"}}`),
		errors.New("insufficient balance: credit exhausted"),
		errors.New("connection reset by peer"),
	}

	for _, err := range cases {
		got := formatProcessingError(err)
		if got == "" {
			t.Fatalf("empty notice for %v", err)
		}
		for _, leak := range []string{"context deadline", "Client.Timeout", "Status:", "Body:", "429", "goroutine", "LLM call failed"} {
			if strings.Contains(got, leak) {
				t.Fatalf("notice leaks technical detail %q: %q", leak, got)
			}
		}
	}
}

func TestFormatProcessingError_InvalidAPIKey(t *testing.T) {
	err := errors.New(
		`LLM call failed after retries: API request failed: Status: 401 Body: {"error":{"message":"Incorrect API key provided"}}`,
	)

	got := formatProcessingError(err)
	if !strings.Contains(got, "Ключ API") {
		t.Fatalf("formatted error missing friendly API key hint: %q", got)
	}
	if strings.Contains(got, "Incorrect API key provided") {
		t.Fatalf("formatted error leaks raw provider body: %q", got)
	}
}

func TestFormatProcessingError_GenericAuthHTTPError(t *testing.T) {
	err := &common.HTTPError{
		StatusCode:  401,
		BodyPreview: `{"error":"unauthorized"}`,
		ContentType: "application/json",
		APIBase:     "https://api.example.com",
	}

	got := formatProcessingError(err)
	if !strings.Contains(got, "авторизации") {
		t.Fatalf("formatted error missing generic auth hint: %q", got)
	}
	if strings.Contains(got, "unauthorized") {
		t.Fatalf("formatted error leaks raw body: %q", got)
	}
}

func TestFormatProcessingError_Timeout(t *testing.T) {
	got := formatProcessingError(errors.New("context deadline exceeded"))
	if !strings.Contains(got, "не ответила вовремя") {
		t.Fatalf("timeout notice = %q", got)
	}
}

func TestFormatProcessingError_NonAuthIsGeneric(t *testing.T) {
	got := formatProcessingError(errors.New("connection reset by peer"))
	want := "⚠️ Не смог обработать сообщение. Технические детали записал в лог."
	if got != want {
		t.Fatalf("formatted error = %q, want %q", got, want)
	}
}

func TestShouldNotifyError_Dedup(t *testing.T) {
	key := "vk|dedup-test"
	if !shouldNotifyError(key) {
		t.Fatal("first notice must be allowed")
	}
	if shouldNotifyError(key) {
		t.Fatal("second notice inside cooldown must be suppressed")
	}
}
