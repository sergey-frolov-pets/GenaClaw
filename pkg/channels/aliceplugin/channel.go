package aliceplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	maxRequestBodySize = 64 << 10 // 64 KiB — Alice requests are small JSON
	defaultWebhookPath = "/webhook/yandex-alice-secure"
	agentTimeout       = 25 * time.Second
)

// Settings is the channel-specific settings block. It is registered with
// config.RegisterChannelSettings so `type: "yandex_alice_secure"` passes config
// validation and the "settings" JSON block decodes into this struct.
type Settings struct {
	// WebhookPath is the HTTP path for the webhook.
	WebhookPath string `json:"webhook_path,omitempty" yaml:"webhook_path,omitempty"`
	// AllowedUserIDs is the allowlist. Empty means NOBODY is allowed.
	AllowedUserIDs []string `json:"allowed_user_ids,omitempty" yaml:"allowed_user_ids,omitempty"`
	// DebugMode makes the skill echo the caller's user_id to unknown users so
	// the owner can enroll their own ID.
	DebugMode bool `json:"debug_mode,omitempty" yaml:"debug_mode,omitempty"`
}

// Channel is the Yandex Alice skill channel (webhook transport).
type Channel struct {
	*channels.BaseChannel

	channelName string
	bc          *config.Channel
	settings    *Settings
	policy      Policy
	client      *http.Client

	mu      sync.Mutex
	pending map[string]chan string // userID -> response channel
}

var _ channels.Channel = (*Channel)(nil)

// New creates the channel. It does not start listening; Start does.
func New(channelName string, bc *config.Channel, s *Settings, messageBus *bus.MessageBus) (*Channel, error) {
	if s == nil {
		s = &Settings{}
	}
	base := channels.NewBaseChannel(
		channelName,
		s,
		messageBus,
		bc.AllowFrom,
		channels.WithMaxMessageLength(1024), // Alice limits TTS to 1024 chars
	)
	return &Channel{
		BaseChannel: base,
		channelName: channelName,
		bc:          bc,
		settings:    s,
		policy:      NewPolicy(s.AllowedUserIDs, s.DebugMode),
		client:      &http.Client{Timeout: 30 * time.Second},
		pending:     make(map[string]chan string),
	}, nil
}

// Start initializes the channel.
func (c *Channel) Start(ctx context.Context) error {
	logger.InfoCF("aliceplugin", "Starting Yandex Alice (secure) webhook channel", map[string]any{
		"webhook_path":       c.WebhookPath(),
		"allowed_user_count": len(c.policy.AllowedUserIDs),
		"debug_mode":         c.policy.DebugMode,
	})
	if len(c.policy.AllowedUserIDs) == 0 && !c.policy.DebugMode {
		logger.WarnC("aliceplugin", "Allowlist is empty: every caller will be refused. "+
			"Enable debug_mode to discover your user_id.")
	}
	c.SetRunning(true)
	return nil
}

// Stop shuts down the channel.
func (c *Channel) Stop(ctx context.Context) error {
	logger.InfoC("aliceplugin", "Stopping Yandex Alice (secure) webhook channel")
	c.SetRunning(false)
	return nil
}

// WebhookPath returns the HTTP path for the Alice webhook.
func (c *Channel) WebhookPath() string {
	if c.settings.WebhookPath != "" {
		return c.settings.WebhookPath
	}
	return defaultWebhookPath
}

// ServeHTTP handles incoming Alice webhook requests.
func (c *Channel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
	if err != nil {
		logger.ErrorCF("aliceplugin", "Failed to read request body", map[string]any{"error": err.Error()})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req AliceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.ErrorCF("aliceplugin", "Failed to parse Alice request", map[string]any{"error": err.Error()})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(req.Session.UserID)

	switch c.policy.Check(userID) {
	case Deny:
		logger.WarnCF("aliceplugin", "Unauthorized user blocked", map[string]any{
			"user_id": userID,
		})
		c.writeResponse(w, &req, "Извините, у вас нет доступа к этому навыку.", true)
		return
	case Debug:
		// Unknown user in debug mode: reveal their ID so they can enroll.
		logger.InfoCF("aliceplugin", "Debug mode: revealing user_id", map[string]any{
			"user_id": userID,
		})
		c.writeResponse(w, &req,
			fmt.Sprintf("Ваш идентификатор: %s. Добавьте его в список разрешённых.", userID), true)
		return
	}

	command := strings.TrimSpace(req.Request.Command)
	if command == "" {
		c.writeResponse(w, &req, "Я вас слушаю.", false)
		return
	}

	logger.InfoCF("aliceplugin", "Received command", map[string]any{
		"user_id": userID,
		"command": command,
		"msg_id":  req.Session.MessageID,
	})

	respCh := make(chan string, 1)
	c.mu.Lock()
	c.pending[userID] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, userID)
		c.mu.Unlock()
	}()

	sender := bus.SenderInfo{
		Platform:    "yandex_alice",
		PlatformID:  userID,
		CanonicalID: userID,
		DisplayName: userID,
	}

	inboundCtx := bus.InboundContext{
		Channel:   c.Name(),
		ChatID:    userID,
		ChatType:  "direct",
		SenderID:  userID,
		MessageID: fmt.Sprintf("%d", req.Session.MessageID),
		Raw: map[string]string{
			"session_id":  req.Session.SessionID,
			"new_session": strconv.FormatBool(req.Session.New),
		},
	}

	if err := c.HandleInboundContext(r.Context(), userID, command, nil, inboundCtx, sender); err != nil {
		logger.ErrorCF("aliceplugin", "Failed to publish inbound message", map[string]any{
			"user_id": userID,
			"error":   err.Error(),
		})
		c.writeResponse(w, &req, "Произошла ошибка, попробуйте позже.", true)
		return
	}

	select {
	case responseText := <-respCh:
		if responseText == "" {
			responseText = "Я не знаю, что ответить."
		}
		c.writeResponse(w, &req, responseText, false)
	case <-r.Context().Done():
		// Client disconnected.
	case <-time.After(agentTimeout):
		logger.WarnCF("aliceplugin", "Timeout waiting for agent response", map[string]any{
			"user_id": userID,
		})
		c.writeResponse(w, &req, "Я задумалась... попробуйте ещё раз.", true)
	}
}

// writeResponse writes a valid Alice JSON response.
func (c *Channel) writeResponse(w http.ResponseWriter, req *AliceRequest, text string, endSession bool) {
	resp := AliceResponse{
		Version: "1.0",
		Session: req.Session,
		Response: AliceResponseBody{
			Text:       text,
			TTS:        text,
			EndSession: endSession,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.ErrorCF("aliceplugin", "Failed to write response", map[string]any{"error": err.Error()})
	}
}

// Send implements the Channel interface. The manager calls this when the agent
// produces an outbound response; we hand it to the waiting ServeHTTP goroutine.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	c.mu.Lock()
	ch, ok := c.pending[msg.ChatID]
	if ok {
		delete(c.pending, msg.ChatID)
	}
	c.mu.Unlock()

	if ok && ch != nil {
		select {
		case ch <- msg.Content:
		default:
			// Already delivered or timed out.
		}
	}
	return nil, nil
}

// =============================================================================
// Alice Protocol Types
// =============================================================================

// AliceRequest is the JSON structure Yandex Alice sends to the webhook.
type AliceRequest struct {
	Meta    AliceMeta    `json:"meta"`
	Session AliceSession `json:"session"`
	Request AliceReqBody `json:"request"`
}

// AliceMeta contains client metadata.
type AliceMeta struct {
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
	ClientID string `json:"client_id"`
}

// AliceSession identifies the conversation.
type AliceSession struct {
	SessionID string `json:"session_id"`
	MessageID int    `json:"message_id"`
	UserID    string `json:"user_id"`
	New       bool   `json:"new"`
}

// AliceReqBody is the user's request.
type AliceReqBody struct {
	Command           string `json:"command"`
	OriginalUtterance string `json:"original_utterance"`
	Type              string `json:"type"`
}

// AliceResponse is the JSON structure sent back to Yandex Alice.
type AliceResponse struct {
	Version  string            `json:"version"`
	Session  AliceSession      `json:"session"`
	Response AliceResponseBody `json:"response"`
}

// AliceResponseBody contains the response text and flags.
type AliceResponseBody struct {
	Text       string `json:"text"`
	TTS        string `json:"tts,omitempty"`
	EndSession bool   `json:"end_session"`
}
