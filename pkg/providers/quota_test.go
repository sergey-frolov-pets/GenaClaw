package providers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestNextDailyQuotaReset(t *testing.T) {
	afternoon := time.Date(2026, time.November, 16, 15, 30, 0, 0, time.UTC)
	want := time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC)
	if got := NextDailyQuotaReset(afternoon); !got.Equal(want) {
		t.Fatalf("reset at 15:30 = %v, want %v", got, want)
	}

	midnight := time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC)
	if got := NextDailyQuotaReset(midnight); !got.Equal(want.AddDate(0, 0, 1)) {
		t.Fatalf("reset at 00:00 = %v, want %v", got, want.AddDate(0, 0, 1))
	}
}

func TestQuotaStore_BlocksAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "model_quota.json")
	qs := NewQuotaStore(path)

	until := time.Now().Add(2 * time.Hour).UTC()
	qs.BlockUntil("model_name:or-x", "or-x", "free_rate_limit", until)

	blocked, remaining := qs.Blocked("model_name:or-x")
	if !blocked {
		t.Fatal("expected candidate to be blocked")
	}
	if remaining <= 0 || remaining > 2*time.Hour {
		t.Fatalf("remaining = %v, want (~2h)", remaining)
	}
	if blocked, _ := qs.Blocked("model_name:other"); blocked {
		t.Fatal("unrelated candidate must not be blocked")
	}

	qs.Clear("model_name:or-x")
	if blocked, _ := qs.Blocked("model_name:or-x"); blocked {
		t.Fatal("expected block to be cleared")
	}
}

func TestQuotaStore_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "model_quota.json")
	until := time.Now().Add(3 * time.Hour).UTC()
	NewQuotaStore(path).BlockUntil("model_name:or-x", "or-x", "free_rate_limit", until)

	// Simulate a gateway restart: brand new store object, same file.
	reloaded := NewQuotaStore(path)
	blocked, remaining := reloaded.Blocked("model_name:or-x")
	if !blocked {
		t.Fatal("block must survive restart")
	}
	if remaining <= time.Hour {
		t.Fatalf("remaining after restart = %v, want > 1h", remaining)
	}
}

func TestQuotaStore_DropsExpiredOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "model_quota.json")
	qs := NewQuotaStore(path)
	qs.BlockUntil("model_name:or-x", "or-x", "free_rate_limit", time.Now().Add(time.Hour))

	// Rewind the block into the past.
	entry := qs.entries["model_name:or-x"]
	entry.BlockedUntil = time.Now().Add(-time.Minute)
	qs.persistLocked()

	if blocked, _ := NewQuotaStore(path).Blocked("model_name:or-x"); blocked {
		t.Fatal("expired block must not be restored")
	}
}

func TestFallback_FreeModelSkippedUntilReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "model_quota.json")

	free := FallbackCandidate{
		Provider:    "openrouter",
		Model:       "inclusionai/ling-3.2-flash:free",
		IdentityKey: "model_name:or-ling-flash",
		ConfigKey:   "config:free",
		Free:        true,
	}
	paid := FallbackCandidate{Provider: "deepseek", Model: "deepseek-flash"}

	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		if c.Free {
			return nil, errors.New("429 too many requests: free-models-per-day limit reached")
		}
		return &LLMResponse{Content: "paid answer", FinishReason: "stop"}, nil
	}

	chain := NewFallbackChain(NewCooldownTracker(), NewRateLimiterRegistry(), WithQuotaStore(NewQuotaStore(path)))
	result, err := chain.ExecuteCandidate(context.Background(), []FallbackCandidate{free, paid}, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", result.Provider)
	}

	// Restart: cooldowns are gone, only the persisted quota block remains.
	freeCalls := 0
	runAfterRestart := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		if c.Free {
			freeCalls++
			return nil, errors.New("429 too many requests")
		}
		return &LLMResponse{Content: "paid answer", FinishReason: "stop"}, nil
	}
	restarted := NewFallbackChain(
		NewCooldownTracker(),
		NewRateLimiterRegistry(),
		WithQuotaStore(NewQuotaStore(path)),
	)
	if _, err := restarted.ExecuteCandidate(
		context.Background(),
		[]FallbackCandidate{free, paid},
		runAfterRestart,
	); err != nil {
		t.Fatalf("unexpected error after restart: %v", err)
	}
	if freeCalls != 0 {
		t.Fatalf("free model was called %d times after quota exhaustion, want 0", freeCalls)
	}
}

func TestFallback_PaidModelRateLimitDoesNotPersistDailyBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "model_quota.json")
	paid := FallbackCandidate{Provider: "deepseek", Model: "deepseek-flash"}

	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		return nil, errors.New("429 too many requests")
	}
	chain := NewFallbackChain(NewCooldownTracker(), NewRateLimiterRegistry(), WithQuotaStore(NewQuotaStore(path)))
	if _, err := chain.ExecuteCandidate(context.Background(), []FallbackCandidate{paid}, run); err == nil {
		t.Fatal("expected fallback exhaustion error")
	}
	if blocked, _ := NewQuotaStore(path).Blocked(paid.StableKey()); blocked {
		t.Fatal("paid model rate limit must not create a daily quota block")
	}
}

func TestFallback_BillingFailureBlocksPaidModelPersistently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "model_quota.json")
	paid := FallbackCandidate{Provider: "deepseek", Model: "deepseek-flash"}

	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		return nil, errors.New("402 payment required: insufficient balance")
	}
	chain := NewFallbackChain(NewCooldownTracker(), NewRateLimiterRegistry(), WithQuotaStore(NewQuotaStore(path)))
	_, _ = chain.ExecuteCandidate(context.Background(), []FallbackCandidate{paid}, run)

	blocked, remaining := NewQuotaStore(path).Blocked(paid.StableKey())
	if !blocked {
		t.Fatal("billing failure must persist a block")
	}
	if remaining < time.Hour {
		t.Fatalf("billing block remaining = %v, want hours", remaining)
	}
}
