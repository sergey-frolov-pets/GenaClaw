package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// QuotaResetHourUTC is the hour (UTC) at which free-tier daily quotas reset.
// OpenRouter free models roll over their daily request budget at 00:00 UTC.
const QuotaResetHourUTC = 0

const quotaStoreVersion = 1

// quotaEntry is one persisted block for a candidate identity.
type quotaEntry struct {
	Key          string    `json:"key"`
	Model        string    `json:"model,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	FreeTier     bool      `json:"free_tier,omitempty"`
	BlockedUntil time.Time `json:"blocked_until"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type quotaFile struct {
	Version int                    `json:"version"`
	Entries map[string]*quotaEntry `json:"entries"`
}

// QuotaStore remembers, across gateway restarts, which model candidates are
// not worth calling: free models whose daily quota is exhausted (blocked until
// the next daily reset instead of being retried every turn) and paid models in
// a billing cooldown.
//
// The store is optional: a nil *QuotaStore is a no-op.
type QuotaStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]*quotaEntry
	nowFunc func() time.Time
}

// NewQuotaStore loads (or creates) the quota state file at path.
// An empty path disables persistence: the store then only works in memory.
func NewQuotaStore(path string) *QuotaStore {
	qs := &QuotaStore{
		path:    path,
		entries: make(map[string]*quotaEntry),
		nowFunc: time.Now,
	}
	qs.load()
	return qs
}

// Path returns the backing file path (empty when persistence is disabled).
func (qs *QuotaStore) Path() string {
	if qs == nil {
		return ""
	}
	return qs.path
}

func (qs *QuotaStore) load() {
	if qs == nil || qs.path == "" {
		return
	}
	data, err := os.ReadFile(qs.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("providers/quota", "failed to read model quota state", map[string]any{
				"path":  qs.path,
				"error": err.Error(),
			})
		}
		return
	}
	var file quotaFile
	if err := json.Unmarshal(data, &file); err != nil {
		logger.WarnCF("providers/quota", "corrupt model quota state, starting fresh", map[string]any{
			"path":  qs.path,
			"error": err.Error(),
		})
		return
	}
	now := qs.nowFunc()
	for key, entry := range file.Entries {
		if entry == nil || key == "" {
			continue
		}
		if !entry.BlockedUntil.After(now) {
			continue // expired while we were down
		}
		qs.entries[key] = entry
	}
}

// Blocked reports whether the candidate identity is still blocked and for how
// long. Expired entries are dropped lazily.
func (qs *QuotaStore) Blocked(key string) (bool, time.Duration) {
	if qs == nil || key == "" {
		return false, 0
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()

	entry := qs.entries[key]
	if entry == nil {
		return false, 0
	}
	now := qs.nowFunc()
	if !entry.BlockedUntil.After(now) {
		delete(qs.entries, key)
		qs.persistLocked()
		return false, 0
	}
	return true, entry.BlockedUntil.Sub(now)
}

// BlockUntil blocks a candidate identity until the given moment.
// Reasons are free-form short labels ("free_rate_limit", "billing").
func (qs *QuotaStore) BlockUntil(key, model, reason string, until time.Time) {
	if qs == nil || key == "" || !until.After(qs.nowFunc()) {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()

	qs.entries[key] = &quotaEntry{
		Key:          key,
		Model:        model,
		Reason:       reason,
		FreeTier:     strings.HasPrefix(reason, "free_"),
		BlockedUntil: until.UTC(),
		UpdatedAt:    qs.nowFunc().UTC(),
	}
	qs.persistLocked()
	logger.InfoCF("providers/quota", "model blocked until quota reset", map[string]any{
		"key":           key,
		"model":         model,
		"reason":        reason,
		"blocked_until": until.UTC().Format(time.RFC3339),
	})
}

// Clear removes the block for a candidate identity (used on a successful call).
func (qs *QuotaStore) Clear(key string) {
	if qs == nil || key == "" {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if _, ok := qs.entries[key]; !ok {
		return
	}
	delete(qs.entries, key)
	qs.persistLocked()
}

// Snapshot returns the currently active blocks sorted by key.
func (qs *QuotaStore) Snapshot() []quotaEntry {
	if qs == nil {
		return nil
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()

	now := qs.nowFunc()
	out := make([]quotaEntry, 0, len(qs.entries))
	for _, entry := range qs.entries {
		if !entry.BlockedUntil.After(now) {
			continue
		}
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (qs *QuotaStore) persistLocked() {
	if qs.path == "" {
		return
	}
	file := quotaFile{Version: quotaStoreVersion, Entries: qs.entries}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		logger.WarnCF("providers/quota", "failed to marshal model quota state", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if dir := filepath.Dir(qs.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			logger.WarnCF("providers/quota", "failed to create quota state directory", map[string]any{
				"dir":   dir,
				"error": err.Error(),
			})
			return
		}
	}
	tmp := qs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.WarnCF("providers/quota", "failed to write model quota state", map[string]any{
			"path":  qs.path,
			"error": err.Error(),
		})
		return
	}
	if err := os.Rename(tmp, qs.path); err != nil {
		logger.WarnCF("providers/quota", "failed to replace model quota state", map[string]any{
			"path":  qs.path,
			"error": err.Error(),
		})
	}
}

// NextDailyQuotaReset returns the next daily free-quota reset moment (UTC).
func NextDailyQuotaReset(now time.Time) time.Time {
	utc := now.UTC()
	year, month, day := utc.Date()
	return time.Date(year, month, day, QuotaResetHourUTC, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// Describe returns a short human-readable block summary for logs and attempts.
func (e quotaEntry) Describe() string {
	left := time.Until(e.BlockedUntil)
	if left < 0 {
		left = 0
	}
	label := e.Reason
	if label == "" {
		label = "quota"
	}
	return fmt.Sprintf("%s blocked (%s, %s remaining)", e.Model, label, left.Round(time.Minute))
}
