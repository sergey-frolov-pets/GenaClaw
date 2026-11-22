package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
)

var isolatedCommandTerminateDuration = 5 * time.Second

// isolatedCommandTransport mirrors the SDK command transport but routes
// process startup through pkg/isolation so Windows post-start hooks run too.
type isolatedCommandTransport struct {
	Command *exec.Cmd
	// Name is the configured MCP server name, used for diagnostics only.
	Name              string
	TerminateDuration time.Duration
}

func (t *isolatedCommandTransport) Connect(ctx context.Context) (sdkmcp.Connection, error) {
	stdout, err := t.Command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdout = io.NopCloser(stdout)
	stderr, err := t.Command.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.Command.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := isolation.Start(t.Command); err != nil {
		return nil, err
	}
	name := t.Name
	if name == "" {
		name = filepath.Base(t.Command.Path)
	}
	go pipeServerStderr(name, stderr)
	td := t.TerminateDuration
	if td <= 0 {
		td = isolatedCommandTerminateDuration
	}
	return newIsolatedIOConn(&isolatedPipeRWC{cmd: t.Command, stdout: stdout, stdin: stdin, terminateDuration: td}), nil
}

// pipeServerStderr forwards the MCP server's stderr into the gateway log.
// Without it the subprocess stderr went to /dev/null and a broken server left
// no trace besides "invalid character ..." on the protocol stream.
func pipeServerStderr(name string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(line) > 500 {
			line = line[:500] + "…"
		}
		fields := map[string]any{"server": name, "line": line}
		if looksLikeServerFailure(line) {
			logger.WarnCF("mcp", "server stderr", fields)
			continue
		}
		logger.DebugCF("mcp", "server stderr", fields)
	}
}

func looksLikeServerFailure(line string) bool {
	l := strings.ToLower(line)
	for _, marker := range []string{"panic:", "fatal", "error", "exception", "unhandled"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

type isolatedPipeRWC struct {
	cmd               *exec.Cmd
	stdout            io.ReadCloser
	stdin             io.WriteCloser
	terminateDuration time.Duration
}

func (s *isolatedPipeRWC) Read(p []byte) (n int, err error) {
	return s.stdout.Read(p)
}

func (s *isolatedPipeRWC) Write(p []byte) (n int, err error) {
	return s.stdin.Write(p)
}

func (s *isolatedPipeRWC) Close() error {
	if err := s.stdin.Close(); err != nil {
		return fmt.Errorf("closing stdin: %w", err)
	}
	resChan := make(chan error, 1)
	go func() {
		resChan <- s.cmd.Wait()
	}()
	wait := func() (error, bool) {
		select {
		case err := <-resChan:
			return err, true
		case <-time.After(s.terminateDuration):
		}
		return nil, false
	}
	if err, ok := wait(); ok {
		return err
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err == nil {
		if err, ok := wait(); ok {
			return err
		}
	}
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	if err, ok := wait(); ok {
		return err
	}
	return fmt.Errorf("unresponsive subprocess")
}

type isolatedIOConn struct {
	writeMu   sync.Mutex
	rwc       io.ReadWriteCloser
	incoming  <-chan isolatedMsgOrErr
	queue     []jsonrpc.Message
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

type isolatedMsgOrErr struct {
	msg json.RawMessage
	err error
}

// nonJSONLogLimit — сколько первых «мусорных» строк stdout логировать подробно.
const nonJSONLogLimit = 5

func newIsolatedIOConn(rwc io.ReadWriteCloser) *isolatedIOConn {
	incoming := make(chan isolatedMsgOrErr)
	closed := make(chan struct{})
	go func() {
		reader := bufio.NewReaderSize(rwc, 64*1024)
		skipped := 0
		send := func(m isolatedMsgOrErr) bool {
			select {
			case incoming <- m:
				return true
			case <-closed:
				return false
			}
		}
		for {
			line, readErr := reader.ReadString('\n')
			if line != "" {
				// Строка не по протоколу (лог Prisma/Node, баннер обёртки) не должна
				// убивать соединение: пропускаем её и читаем дальше.
				if raw, ok := parseServerLine(line, &skipped); ok {
					if !send(isolatedMsgOrErr{msg: raw}) {
						return
					}
				}
			}
			if readErr != nil {
				send(isolatedMsgOrErr{err: readErr})
				return
			}
		}
	}()
	return &isolatedIOConn{rwc: rwc, incoming: incoming, closed: closed}
}

// parseServerLine отдаёт сообщение, только если строка — валидный JSON-RPC.
// Всё остальное считается шумом сервера и пишется в лог (без спама).
func parseServerLine(line string, skipped *int) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, false
	}
	raw := json.RawMessage(trimmed)
	if !json.Valid([]byte(trimmed)) {
		*skipped = *skipped + 1
		logSkippedServerLine(trimmed, *skipped)
		return nil, false
	}
	if _, err := readIsolatedBatch(raw); err != nil {
		*skipped = *skipped + 1
		logSkippedServerLine(trimmed, *skipped)
		return nil, false
	}
	return raw, true
}

func logSkippedServerLine(line string, count int) {
	if count > nonJSONLogLimit && count%100 != 0 {
		return
	}
	if len(line) > 300 {
		line = line[:300] + "…"
	}
	logger.DebugCF("mcp", "skipped non-JSON line from server stdout",
		map[string]any{"line": line, "skipped_total": count})
}

func (c *isolatedIOConn) SessionID() string { return "" }

func (c *isolatedIOConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if len(c.queue) > 0 {
		next := c.queue[0]
		c.queue = c.queue[1:]
		return next, nil
	}
	var raw json.RawMessage
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case v := <-c.incoming:
		if v.err != nil {
			return nil, v.err
		}
		raw = v.msg
	case <-c.closed:
		return nil, io.EOF
	}
	msgs, err := readIsolatedBatch(raw)
	if err != nil {
		return nil, err
	}
	c.queue = msgs[1:]
	return msgs[0], nil
}

func readIsolatedBatch(data []byte) ([]jsonrpc.Message, error) {
	var rawBatch []json.RawMessage
	if err := json.Unmarshal(data, &rawBatch); err == nil {
		if len(rawBatch) == 0 {
			return nil, fmt.Errorf("empty batch")
		}
		msgs := make([]jsonrpc.Message, 0, len(rawBatch))
		for _, raw := range rawBatch {
			msg, err := jsonrpc.DecodeMessage(raw)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, msg)
		}
		return msgs, nil
	}
	msg, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil, err
	}
	return []jsonrpc.Message{msg}, nil
}

func (c *isolatedIOConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	data = append(data, '\n')
	_, err = c.rwc.Write(data)
	return err
}

func (c *isolatedIOConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.rwc.Close()
		close(c.closed)
	})
	return c.closeErr
}

var (
	_ sdkmcp.Transport  = (*isolatedCommandTransport)(nil)
	_ sdkmcp.Connection = (*isolatedIOConn)(nil)
)
