package agent

import (
	"strings"
	"testing"
)

func TestDailyHeadlinesTitlesAndTodaySnippet(t *testing.T) {
	notes := `# 2026-11-25

## 10:00 — Gena: плашка под кнопками
Сергей: «плашка под кнопками токенов».
Исправлено в fec39b9.

## 12:00 — диск: освобождено 1.7 ГБ
Почистил кэш.

> цитата в блоке
`
	today := DailyHeadlines(notes, true, 40)
	if !strings.Contains(today, "- 10:00 — Gena: плашка под кнопками") {
		t.Fatalf("title missing: %q", today)
	}
	if !strings.Contains(today, "Сергей: «плашка под кнопками токено") {
		t.Fatalf("snippet missing or not truncated: %q", today)
	}
	if strings.Contains(today, "Исправлено в fec39b9") {
		t.Fatalf("only the first body line may be included")
	}
	if strings.Contains(today, "# 2026-11-25") {
		t.Fatalf("day header must be dropped")
	}

	old := DailyHeadlines(notes, false, 40)
	if strings.Contains(old, "Сергей") {
		t.Fatalf("older days must carry titles only: %q", old)
	}
	if !strings.Contains(old, "- 12:00 — диск: освобождено 1.7 ГБ") {
		t.Fatalf("second title missing: %q", old)
	}
}

func TestDailyHeadlinesEmpty(t *testing.T) {
	if got := DailyHeadlines("   \n", true, 10); got != "" {
		t.Fatalf("empty notes must render nothing, got %q", got)
	}
}

func TestGetMemoryContextOffKeepsLongTerm(t *testing.T) {
	ms := &MemoryStore{memoryDir: t.TempDir()}
	ms.memoryFile = ms.memoryDir + "/MEMORY.md"
	if err := ms.WriteLongTerm("индекс доменов"); err != nil {
		t.Fatalf("write long term: %v", err)
	}

	got := ms.GetMemoryContextWithOptions("off", 3)
	if !strings.Contains(got, "индекс доменов") {
		t.Fatalf("long-term memory must stay: %q", got)
	}
	if strings.Contains(got, "Recent Daily Notes") {
		t.Fatalf("daily notes must be off: %q", got)
	}
}
