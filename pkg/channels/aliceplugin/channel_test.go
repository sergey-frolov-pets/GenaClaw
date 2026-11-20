package aliceplugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// newTestChannel builds a channel without a message bus; only the access-control
// paths are exercised here, so HandleInboundContext is never reached.
func newTestChannel(t *testing.T, s *Settings) *Channel {
	t.Helper()
	ch, err := New("yandex_alice_secure", &config.Channel{}, s, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = ch.Start(nil)
	return ch
}

func aliceBody(userID, command string) string {
	req := AliceRequest{
		Session: AliceSession{SessionID: "s1", MessageID: 1, UserID: userID, New: true},
		Request: AliceReqBody{Command: command, Type: "SimpleUtterance"},
	}
	b, _ := json.Marshal(req)
	return string(b)
}

func doRequest(t *testing.T, ch *Channel, body string) AliceResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, ch.WebhookPath(), strings.NewReader(body))
	ch.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp AliceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON response: %v (%s)", err, rec.Body.String())
	}
	return resp
}

func TestServeHTTP_BlocksUnknownUser(t *testing.T) {
	ch := newTestChannel(t, &Settings{AllowedUserIDs: []string{"owner"}})
	resp := doRequest(t, ch, aliceBody("intruder", "привет"))

	if !resp.Response.EndSession {
		t.Fatal("unknown user must get end_session=true")
	}
	if !strings.Contains(resp.Response.Text, "нет доступа") {
		t.Fatalf("expected refusal text, got %q", resp.Response.Text)
	}
}

func TestServeHTTP_BlocksEveryoneWhenAllowlistEmpty(t *testing.T) {
	ch := newTestChannel(t, &Settings{})
	resp := doRequest(t, ch, aliceBody("anybody", "привет"))

	if !resp.Response.EndSession {
		t.Fatal("empty allowlist must refuse with end_session=true")
	}
	if !strings.Contains(resp.Response.Text, "нет доступа") {
		t.Fatalf("expected refusal text, got %q", resp.Response.Text)
	}
}

func TestServeHTTP_DebugModeRevealsUserID(t *testing.T) {
	ch := newTestChannel(t, &Settings{DebugMode: true})
	resp := doRequest(t, ch, aliceBody("my-yandex-id", "привет"))

	if !resp.Response.EndSession {
		t.Fatal("debug reply must end the session")
	}
	if !strings.Contains(resp.Response.Text, "my-yandex-id") {
		t.Fatalf("debug reply must contain the user_id, got %q", resp.Response.Text)
	}
}

func TestServeHTTP_MissingUserIDDenied(t *testing.T) {
	ch := newTestChannel(t, &Settings{DebugMode: true})
	resp := doRequest(t, ch, aliceBody("", "привет"))

	if !resp.Response.EndSession {
		t.Fatal("missing user_id must end the session")
	}
	if strings.Contains(resp.Response.Text, "идентификатор") {
		t.Fatalf("must not reveal an empty id, got %q", resp.Response.Text)
	}
}

func TestServeHTTP_AllowedUserEmptyCommandGetsPrompt(t *testing.T) {
	ch := newTestChannel(t, &Settings{AllowedUserIDs: []string{"owner"}})
	resp := doRequest(t, ch, aliceBody("owner", ""))

	if resp.Response.EndSession {
		t.Fatal("empty command should keep the session open")
	}
	if !strings.Contains(resp.Response.Text, "слушаю") {
		t.Fatalf("expected listening prompt, got %q", resp.Response.Text)
	}
}

func TestServeHTTP_RejectsNonPost(t *testing.T) {
	ch := newTestChannel(t, &Settings{AllowedUserIDs: []string{"owner"}})
	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ch.WebhookPath(), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestServeHTTP_RejectsBadJSON(t *testing.T) {
	ch := newTestChannel(t, &Settings{AllowedUserIDs: []string{"owner"}})
	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ch.WebhookPath(), strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestWebhookPath_Default(t *testing.T) {
	ch := newTestChannel(t, &Settings{})
	if ch.WebhookPath() != defaultWebhookPath {
		t.Fatalf("expected %q, got %q", defaultWebhookPath, ch.WebhookPath())
	}
}

func TestWebhookPath_Custom(t *testing.T) {
	ch := newTestChannel(t, &Settings{WebhookPath: "/alice"})
	if ch.WebhookPath() != "/alice" {
		t.Fatalf("expected /alice, got %q", ch.WebhookPath())
	}
}
