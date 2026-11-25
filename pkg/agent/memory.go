// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/fileutil"
)

// MemoryStore manages persistent memory for the agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
}

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	os.MkdirAll(memoryDir, 0o755)

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102") // YYYYMMDD
	monthDir := today[:6]                  // YYYYMM
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
func (ms *MemoryStore) WriteLongTerm(content string) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(ms.memoryFile, []byte(content), 0o600)
}

// ReadToday reads today's daily note.
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadToday() string {
	todayFile := ms.getTodayFile()
	if data, err := os.ReadFile(todayFile); err == nil {
		return string(data)
	}
	return ""
}

// AppendToday appends content to today's daily note.
// If the file doesn't exist, it creates a new file with a date header.
func (ms *MemoryStore) AppendToday(content string) error {
	todayFile := ms.getTodayFile()

	// Ensure month directory exists
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return err
	}

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		// Add header for new day
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		// Append to existing content
		newContent = existingContent + "\n" + content
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(todayFile, []byte(newContent), 0o600)
}

// GetRecentDailyNotes returns daily notes from the last N days.
// Contents are joined with "---" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var sb strings.Builder
	first := true

	for i := range days {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		monthDir := dateStr[:6]            // YYYYMM
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		if data, err := os.ReadFile(filePath); err == nil {
			if !first {
				sb.WriteString("\n\n---\n\n")
			}
			sb.Write(data)
			first = false
		}
	}

	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent prompt.
// Includes long-term memory and recent daily notes.
func (ms *MemoryStore) GetMemoryContext() string {
	return ms.GetMemoryContextWithOptions("full", 3)
}

// DailyHeadlines condenses daily notes into record titles (plus the first body
// line for the most recent day). This keeps the prompt small while still
// telling the model what happened and where the full text lives.
//
// Two runs of the same notes in a long session are pure noise, so snippets are
// only taken from the newest day; older days contribute titles only.
func DailyHeadlines(text string, withSnippets bool, maxSnippet int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if maxSnippet <= 0 {
		maxSnippet = 180
	}

	var sb strings.Builder
	inRecord := false
	snippetTaken := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			inRecord = true
			snippetTaken = false
			sb.WriteString("- " + strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")) + "\n")
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			// Day header, already known from the caller.
			continue
		}
		if !inRecord || snippetTaken || !withSnippets {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "> ") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		if len([]rune(trimmed)) > maxSnippet {
			trimmed = string([]rune(trimmed)[:maxSnippet]) + "…"
		}
		sb.WriteString("  " + trimmed + "\n")
		snippetTaken = true
	}

	return strings.TrimRight(sb.String(), "\n")
}

// GetMemoryContextWithOptions builds the memory block for the system prompt.
// mode: "full" (whole daily notes), "headlines" (titles + today's first lines), "off".
func (ms *MemoryStore) GetMemoryContextWithOptions(mode string, days int) string {
	longTerm := ms.ReadLongTerm()
	if days <= 0 {
		days = 3
	}

	var notes strings.Builder
	if mode != "off" {
		for i := range days {
			date := time.Now().AddDate(0, 0, -i)
			dateStr := date.Format("20060102")
			filePath := filepath.Join(ms.memoryDir, dateStr[:6], dateStr+".md")
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			rel := filepath.ToSlash(filepath.Join("memory", dateStr[:6], dateStr+".md"))
			if mode == "headlines" {
				body := DailyHeadlines(string(data), i == 0, 180)
				if strings.TrimSpace(body) == "" {
					continue
				}
				fmt.Fprintf(&notes, "### %s (%s)\n%s\nполный текст: read_file %s\n\n", date.Format("2006-01-02"), rel, body, rel)
				continue
			}
			fmt.Fprintf(&notes, "### %s\n%s\n\n", date.Format("2006-01-02"), strings.TrimSpace(string(data)))
		}
	}

	if longTerm == "" && notes.Len() == 0 {
		return ""
	}

	var sb strings.Builder

	if longTerm != "" {
		sb.WriteString("## Long-term Memory\n\n")
		sb.WriteString(longTerm)
	}

	if notes.Len() > 0 {
		if longTerm != "" {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Recent Daily Notes\n\n")
		if mode == "headlines" {
			sb.WriteString("Записи сокращены до заголовков. Нужны детали — прочитай файл по ссылке или используй поиск по памяти.\n\n")
		}
		sb.WriteString(notes.String())
	}

	return sb.String()
}
