package uiblock

import (
	"encoding/json"
	"strings"
	"testing"
)

const validBlock = `{"v":1,"blockId":"oz-1","plugin":"ozadachnik.tasks","title":"Задачи","data":{"view":"list","items":[{"id":"a1","title":"План"},{"id":"a2","title":"Токен"}]},"actions":[{"id":"done","label":"Готово","scope":"item","send":"задача {id} выполнена"}]}`

func fence(body string) string {
	return "```gena-ui\n" + body + "\n```"
}

func TestExtractNoFence(t *testing.T) {
	text := "Обычный ответ без карточки."
	got := Extract(text)
	if got.HasBlock() {
		t.Fatalf("unexpected block: %s", got.Block)
	}
	if got.Clean != text {
		t.Fatalf("clean = %q, want %q", got.Clean, text)
	}
	if got.Dropped != "" {
		t.Fatalf("dropped = %q, want empty", got.Dropped)
	}
}

func TestExtractValidBlockIsLast(t *testing.T) {
	text := "Задачи на сегодня: 7.\n\n" + fence(validBlock)
	got := Extract(text)
	if !got.HasBlock() {
		t.Fatalf("block not found, dropped = %q", got.Dropped)
	}
	if got.Plugin != "ozadachnik.tasks" {
		t.Fatalf("plugin = %q", got.Plugin)
	}
	if got.Clean != "Задачи на сегодня: 7." {
		t.Fatalf("clean = %q", got.Clean)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Block, &payload); err != nil {
		t.Fatalf("block is not valid JSON: %v", err)
	}
}

func TestExtractBlockKeepsSurroundingText(t *testing.T) {
	text := "До блока.\n\n" + fence(validBlock) + "\n\nПосле блока."
	got := Extract(text)
	if !got.HasBlock() {
		t.Fatalf("block not found")
	}
	if strings.Contains(got.Clean, FenceInfo) {
		t.Fatalf("fence left in clean text: %q", got.Clean)
	}
	if !strings.Contains(got.Clean, "До блока.") || !strings.Contains(got.Clean, "После блока.") {
		t.Fatalf("surrounding text lost: %q", got.Clean)
	}
}

func TestExtractInvalidJSONIsRemovedWithMarker(t *testing.T) {
	text := "Резюме.\n\n" + fence(`{"v":1,"plugin":"x","data":{`)
	got := Extract(text)
	if got.HasBlock() {
		t.Fatalf("invalid block accepted")
	}
	if got.Dropped == "" {
		t.Fatalf("reason not reported")
	}
	if strings.Contains(got.Clean, FenceInfo) || strings.Contains(got.Clean, `"plugin"`) {
		t.Fatalf("json left in clean text: %q", got.Clean)
	}
	if !strings.Contains(got.Clean, "[ui-блок отброшен:") {
		t.Fatalf("marker missing: %q", got.Clean)
	}
	if !strings.Contains(got.Clean, "Резюме.") {
		t.Fatalf("summary lost: %q", got.Clean)
	}
}

func TestExtractTwoBlocksUsesLast(t *testing.T) {
	first := strings.Replace(validBlock, "ozadachnik.tasks", "backlog.board", 1)
	text := fence(first) + "\n\nМежду блоками.\n\n" + fence(validBlock)
	got := Extract(text)
	if got.Plugin != "ozadachnik.tasks" {
		t.Fatalf("plugin = %q, want last block", got.Plugin)
	}
	if strings.Contains(got.Clean, FenceInfo) || strings.Contains(got.Clean, "backlog.board") {
		t.Fatalf("first block left in text: %q", got.Clean)
	}
	if !strings.Contains(got.Clean, "Между блоками.") {
		t.Fatalf("text between blocks lost: %q", got.Clean)
	}
}

func TestExtractUnclosedFenceAtEnd(t *testing.T) {
	text := "Резюме.\n\n```gena-ui\n" + validBlock
	got := Extract(text)
	if !got.HasBlock() {
		t.Fatalf("unclosed fence not treated as block, dropped = %q", got.Dropped)
	}
	if got.Clean != "Резюме." {
		t.Fatalf("clean = %q", got.Clean)
	}
}

func TestExtractOtherCodeFenceUntouched(t *testing.T) {
	text := "Пример:\n\n```json\n{\"a\":1}\n```"
	got := Extract(text)
	if got.HasBlock() || got.Dropped != "" {
		t.Fatalf("foreign fence touched: %+v", got)
	}
	if !strings.Contains(got.Clean, "```json") {
		t.Fatalf("foreign fence removed: %q", got.Clean)
	}
}

func TestExtractFenceWithExtraSpaces(t *testing.T) {
	text := "Резюме.\n\n```  gena-ui  \n" + validBlock + "\n```"
	got := Extract(text)
	if !got.HasBlock() {
		t.Fatalf("fence with spaces not recognised, dropped = %q", got.Dropped)
	}
}

func TestExtractCRLF(t *testing.T) {
	text := "Резюме.\r\n\r\n```gena-ui\r\n" + validBlock + "\r\n```"
	got := Extract(text)
	if !got.HasBlock() {
		t.Fatalf("CRLF fence not recognised")
	}
	if got.Clean != "Резюме." {
		t.Fatalf("clean = %q", got.Clean)
	}
}

func TestExtractOversizedBlockRejected(t *testing.T) {
	big := strings.Replace(validBlock, `"title":"План"`, `"title":"`+strings.Repeat("x", MaxBytes)+`"`, 1)
	got := Extract("Резюме.\n\n" + fence(big))
	if got.HasBlock() {
		t.Fatalf("oversized block accepted")
	}
	if !strings.Contains(got.Dropped, "КБ") {
		t.Fatalf("reason = %q", got.Dropped)
	}
}

func TestExtractUnknownVersionRejected(t *testing.T) {
	block := strings.Replace(validBlock, `"v":1`, `"v":9`, 1)
	got := Extract("Резюме.\n\n" + fence(block))
	if got.HasBlock() {
		t.Fatalf("unknown version accepted")
	}
	if !strings.Contains(got.Dropped, "версия") {
		t.Fatalf("reason = %q", got.Dropped)
	}
}

func TestExtractMissingPluginRejected(t *testing.T) {
	got := Extract("Резюме.\n\n" + fence(`{"v":1,"data":{"view":"list"}}`))
	if got.HasBlock() {
		t.Fatalf("block without plugin accepted")
	}
	if !strings.Contains(got.Dropped, "plugin") {
		t.Fatalf("reason = %q", got.Dropped)
	}
}

func TestExtractTooManyItemsRejected(t *testing.T) {
	items := make([]string, 0, MaxItems+1)
	for i := 0; i <= MaxItems; i++ {
		items = append(items, `{"id":"i","title":"t"}`)
	}
	block := `{"v":1,"plugin":"x","data":{"view":"list","items":[` + strings.Join(items, ",") + `]}}`
	got := Extract("Резюме.\n\n" + fence(block))
	if got.HasBlock() {
		t.Fatalf("block with too many items accepted")
	}
	if !strings.Contains(got.Dropped, "элементов") {
		t.Fatalf("reason = %q", got.Dropped)
	}
}

func TestExtractEmptyBodyRejected(t *testing.T) {
	got := Extract("Резюме.\n\n```gena-ui\n\n```")
	if got.HasBlock() {
		t.Fatalf("empty block accepted")
	}
	if !strings.HasPrefix(got.Clean, "Резюме.") {
		t.Fatalf("summary lost: %q", got.Clean)
	}
	if !strings.Contains(got.Clean, "[ui-блок отброшен: пустой блок]") {
		t.Fatalf("marker missing: %q", got.Clean)
	}
}

func TestExtractOnlyBlockGivesEmptyClean(t *testing.T) {
	got := Extract(fence(validBlock))
	if !got.HasBlock() {
		t.Fatalf("block not found")
	}
	if got.Clean != "" {
		t.Fatalf("clean = %q, want empty", got.Clean)
	}
}

func TestExtractNoFenceFastPathKeepsText(t *testing.T) {
	text := strings.Repeat("строка без ограждения\n", 100)
	got := Extract(text)
	if got.Clean != text {
		t.Fatalf("text changed on fast path")
	}
}

func TestExtractProseMentionKeepsTextVerbatim(t *testing.T) {
	text := "Скилл называется gena-ui.\r\n\n\n\nОн рисует карточки.   \n"
	got := Extract(text)
	if got.HasBlock() || got.Dropped != "" {
		t.Fatalf("prose mention treated as a block: %+v", got)
	}
	if got.Clean != text {
		t.Fatalf("text changed on prose mention:\n got %q\nwant %q", got.Clean, text)
	}
}
