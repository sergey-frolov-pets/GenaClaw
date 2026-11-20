package yandexalice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const maxRequestBodySize = 64 << 10 // 64 KiB — Alice requests are small JSON

// YandexAliceChannel handles incoming requests from Yandex Alice skill webhook.
type YandexAliceChannel struct {
	*channels.BaseChannel
	config *config.YandexAliceSettings
	client *http.Client

	mu      sync.Mutex
	pending map[string]chan string // userID → response channel
}

// NewYandexAliceChannel creates a new Yandex Alice webhook channel.
func NewYandexAliceChannel(
	bc *config.Channel,
	cfg *config.YandexAliceSettings,
	messageBus *bus.MessageBus,
) (*YandexAliceChannel, error) {
	base := channels.NewBaseChannel(
		"yandex_alice",
		cfg,
		messageBus,
		bc.AllowFrom,
		channels.WithMaxMessageLength(1024), // Alice limits TTS to 1024 chars
	)

	return &YandexAliceChannel{
		BaseChannel: base,
		config:      cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		pending: make(map[string]chan string),
	}, nil
}

// Start initializes the channel.
func (c *YandexAliceChannel) Start(ctx context.Context) error {
	logger.InfoCF("yandex_alice", "Starting Yandex Alice webhook channel", map[string]any{
		"webhook_path":       c.WebhookPath(),
		"allowed_user_count": len(c.config.AllowedUserIDs),
	})
	c.SetRunning(true)
	return nil
}

// Stop shuts down the channel.
func (c *YandexAliceChannel) Stop(ctx context.Context) error {
	logger.InfoC("yandex_alice", "Stopping Yandex Alice webhook channel")
	c.SetRunning(false)
	return nil
}

// WebhookPath returns the HTTP path for Alice webhook.
func (c *YandexAliceChannel) WebhookPath() string {
	if c.config.WebhookPath != "" {
		return c.config.WebhookPath
	}
	return "/webhook/yandex-alice"
}

// ServeHTTP handles incoming Alice webhook requests.
func (c *YandexAliceChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
	if err != nil {
		logger.ErrorCF("yandex_alice", "Failed to read request body", map[string]any{"error": err.Error()})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req AliceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.ErrorCF("yandex_alice", "Failed to parse Alice request", map[string]any{"error": err.Error()})
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	userID := req.Session.UserID
	if userID == "" {
		logger.WarnC("yandex_alice", "Request missing user_id")
		c.writeResponse(w, &req, "Ошибка: не удалось определить пользователя.", true)
		return
	}

	// Auth check
	if !c.isAllowed(userID) {
		logger.WarnCF("yandex_alice", "Unauthorized user blocked", map[string]any{
			"user_id": userID,
		})
		c.writeResponse(w, &req, "Извините, у вас нет доступа к этому навыку.", true)
		return
	}

	command := strings.TrimSpace(req.Request.Command)
	if command == "" {
		c.writeResponse(w, &req, "Я вас слушаю.", false)
		return
	}

	logger.InfoCF("yandex_alice", "Received command", map[string]any{
		"user_id": userID,
		"command": command,
		"msg_id":  req.Session.MessageID,
	})

	// Create response channel and register it
	respCh := make(chan string, 1)
	c.mu.Lock()
	c.pending[userID] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, userID)
		c.mu.Unlock()
	}()

	// Build inbound context and publish
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
		Raw: map[string]any{
			"session_id":  req.Session.SessionID,
			"new_session": req.Session.New,
		},
	}

	if err := c.HandleInboundContext(r.Context(), userID, command, nil, inboundCtx, sender); err != nil {
		logger.ErrorCF("yandex_alice", "Failed to publish inbound message", map[string]any{
			"user_id": userID,
			"error":   err.Error(),
		})
		c.writeResponse(w, &req, "Произошла ошибка, попробуйте позже.", true)
		return
	}

	// Wait for response from agent (via Send)
	select {
	case responseText := <-respCh:
		if responseText == "" {
			responseText = "Я не знаю, что ответить."
		}
		c.writeResponse(w, &req, responseText, false)
	case <-r.Context().Done():
		// Client disconnected
	case <-time.After(25 * time.Second):
		logger.WarnCF("yandex_alice", "Timeout waiting for agent response", map[string]any{
			"user_id": userID,
		})
		c.writeResponse(w, &req, "Я задумалась... попробуйте ещё раз.", true)
	}
}

// isAllowed checks if the user_id is in the allowed list.
// If the allowed list is empty, all users are allowed (development mode).
func (c *YandexAliceChannel) isAllowed(userID string) bool {
	if len(c.config.AllowedUserIDs) == 0 {
		return true
	}
	for _, id := range c.config.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// writeResponse writes a valid Alice JSON response.
func (c *YandexAliceChannel) writeResponse(w http.ResponseWriter, req *AliceRequest, text string, endSession bool) {
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
		logger.ErrorCF("yandex_alice", "Failed to write response", map[string]any{"error": err.Error()})
	}
}

// Send implements the Channel interface. The manager calls this when the agent
// produces an outbound response. We deliver it to the waiting ServeHTTP goroutine.
func (c *YandexAliceChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
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
			// Channel full or closed — response already delivered or timed out
		}
	}

	// Return nil — the response was already delivered to Alice via the HTTP handler.
	// No message IDs to return since Alice doesn't use them.
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
