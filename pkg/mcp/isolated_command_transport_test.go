package mcp

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// scriptedRWC отдаёт заранее заданный поток «как из stdout сервера».
type scriptedRWC struct {
	mu     sync.Mutex
	r      *strings.Reader
	writes bytes.Buffer
}

func newScriptedRWC(stream string) *scriptedRWC {
	return &scriptedRWC{r: strings.NewReader(stream)}
}

func (s *scriptedRWC) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.Read(p)
}

func (s *scriptedRWC) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes.Write(p)
}

func (s *scriptedRWC) Close() error { return nil }

func readResponseID(t *testing.T, conn *isolatedIOConn) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	resp, ok := msg.(*jsonrpc.Response)
	if !ok {
		t.Fatalf("want *jsonrpc.Response, got %T", msg)
	}
	id, _ := resp.ID.Raw().(int64)
	return id
}

// Мусор в stdout (лог / баннер обёртки) раньше ронял декодер и навсегда убивал
// соединение: «invalid character 'p' looking for beginning of value».
func TestIsolatedIOConnSkipsNoiseBeforeMessage(t *testing.T) {
	stream := "prisma:info PostgreSQL pool started\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n"
	conn := newIsolatedIOConn(newScriptedRWC(stream))
	defer conn.Close()

	if id := readResponseID(t, conn); id != 1 {
		t.Fatalf("want id 1, got %d", id)
	}
}

// Валидный JSON, но не JSON-RPC, тоже не должен рвать соединение.
func TestIsolatedIOConnSkipsNonRPCJSON(t *testing.T) {
	stream := "{\"hello\":\"world\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\n"
	conn := newIsolatedIOConn(newScriptedRWC(stream))
	defer conn.Close()

	if id := readResponseID(t, conn); id != 2 {
		t.Fatalf("want id 2, got %d", id)
	}
}

// Несколько сообщений подряд (в т.ч. после мусора) читаются по одному.
func TestIsolatedIOConnReadsStreamAfterNoise(t *testing.T) {
	stream := "picoclaw: starting MCP server wrapper\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{}}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":4,\"result\":{}}\n"
	conn := newIsolatedIOConn(newScriptedRWC(stream))
	defer conn.Close()

	for _, want := range []int64{3, 4} {
		if id := readResponseID(t, conn); id != want {
			t.Fatalf("want id %d, got %d", want, id)
		}
	}
}

// Конец потока после мусора отдаётся как EOF, а не «зависшее» соединение.
func TestIsolatedIOConnEOFSurfacesAsError(t *testing.T) {
	conn := newIsolatedIOConn(newScriptedRWC("only noise, no protocol frames\n"))
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := conn.Read(ctx); err == nil {
		t.Fatal("expected error on EOF without protocol frames")
	} else if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

// Последнее сообщение без завершающего перевода строки не теряется.
func TestIsolatedIOConnHandlesFinalLineWithoutNewline(t *testing.T) {
	conn := newIsolatedIOConn(newScriptedRWC("{\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}"))
	defer conn.Close()

	if id := readResponseID(t, conn); id != 7 {
		t.Fatalf("want id 7, got %d", id)
	}
}
