package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/google/uuid"
)

type Config struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	TokenFile string `json:"token_file"`
	Listen    bool   `json:"listen"`
}

type Plugin struct {
	cfg    Config
	token  string
	client *http.Client
	log    *slog.Logger

	// Source fields (only used when Listen is true).
	bus      plugin.EventBus
	botID    string
	botName  string
	wsCancel context.CancelFunc
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{},
		log:    log,
	}
}

func (p *Plugin) Name() string { return "mattermost" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("mattermost config: %w", err)
	}

	p.token = p.cfg.Token
	if p.cfg.TokenFile != "" {
		token, err := os.ReadFile(p.cfg.TokenFile)
		if err != nil {
			return fmt.Errorf("reading mattermost token: %w", err)
		}
		p.token = strings.TrimSpace(string(token))
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	p.bus = bus
	if !p.cfg.Listen {
		return nil
	}

	if err := p.fetchBotUser(ctx); err != nil {
		return fmt.Errorf("mattermost: fetch bot user: %w", err)
	}
	p.log.Info("mattermost: listening as bot", "bot_id", p.botID, "bot_name", p.botName)

	wsCtx, cancel := context.WithCancel(ctx)
	p.wsCancel = cancel
	go p.listenWS(wsCtx)
	return nil
}

func (p *Plugin) Stop() error {
	if p.wsCancel != nil {
		p.wsCancel()
	}
	return nil
}

// fetchBotUser calls GET /api/v4/users/me to learn the bot's own user ID and username.
func (p *Plugin) fetchBotUser(ctx context.Context) error {
	u, err := url.JoinPath(p.cfg.URL, "/api/v4/users/me")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error %d: %s", resp.StatusCode, string(body))
	}

	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return err
	}
	p.botID = me.ID
	p.botName = me.Username
	return nil
}

// listenWS is the outer reconnection loop with exponential backoff.
func (p *Plugin) listenWS(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		start := time.Now()
		err := p.connectAndListen(ctx)
		if ctx.Err() != nil {
			return
		}
		p.log.Error("mattermost: websocket disconnected", "error", err)

		// Reset backoff if the connection was stable for >60s.
		if time.Since(start) > 60*time.Second {
			backoff = time.Second
		}

		p.log.Info("mattermost: reconnecting", "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndListen dials the Mattermost WebSocket, authenticates, and reads events.
func (p *Plugin) connectAndListen(ctx context.Context) error {
	wsURL := buildWSURL(p.cfg.URL)
	p.log.Debug("mattermost: dialing websocket", "url", wsURL)

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	// Authenticate via the WebSocket auth challenge.
	authMsg, _ := json.Marshal(map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]string{"token": p.token},
	})
	if err := conn.Write(ctx, websocket.MessageText, authMsg); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	p.log.Info("mattermost: websocket connected")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		p.handleWSMessage(data)
	}
}

// buildWSURL converts an HTTP(S) base URL to the Mattermost WebSocket endpoint.
func buildWSURL(base string) string {
	s := strings.Replace(base, "https://", "wss://", 1)
	s = strings.Replace(s, "http://", "ws://", 1)
	s = strings.TrimRight(s, "/")
	return s + "/api/v4/websocket"
}

// Mattermost WebSocket event envelope.
type wsEvent struct {
	Event     string      `json:"event"`
	Data      wsEventData `json:"data"`
	Broadcast wsBroadcast `json:"broadcast"`
}

type wsEventData struct {
	Post        string `json:"post"`         // JSON string of the post object
	ChannelType string `json:"channel_type"` // "D" for DM, "O" for open, etc.
	SenderName  string `json:"sender_name"`
}

type wsBroadcast struct {
	ChannelID string `json:"channel_id"`
}

type wsPost struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	RootID    string `json:"root_id"`
}

func (p *Plugin) handleWSMessage(data []byte) {
	var ev wsEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	if ev.Event != "posted" {
		return
	}

	var post wsPost
	if err := json.Unmarshal([]byte(ev.Data.Post), &post); err != nil {
		p.log.Error("mattermost: parse post", "error", err)
		return
	}

	// Ignore our own messages to prevent loops.
	if post.UserID == p.botID {
		return
	}

	// Only emit for DMs or @mentions.
	isDM := ev.Data.ChannelType == "D"
	isMention := strings.Contains(post.Message, "@"+p.botName)
	if !isDM && !isMention {
		return
	}

	p.log.Info("mattermost: incoming message",
		"channel_id", post.ChannelID,
		"user_id", post.UserID,
		"is_dm", isDM,
		"is_mention", isMention,
	)

	p.bus.Emit(plugin.Event{
		ID:        uuid.NewString(),
		Source:    "mattermost",
		Type:      "message",
		Timestamp: time.Now(),
		Payload: map[string]any{
			"channel":      post.ChannelID,
			"channel_id":   post.ChannelID,
			"post_id":      post.ID,
			"root_id":      post.RootID,
			"message":      post.Message,
			"user_id":      post.UserID,
			"sender_name":  ev.Data.SenderName,
			"channel_type": ev.Data.ChannelType,
		},
	})
}

// --- Sink ---

func (p *Plugin) HandleEvent(ctx context.Context, event plugin.Event) error {
	channel, _ := event.Payload["channel"].(string)
	if channel == "" {
		return fmt.Errorf("mattermost: no channel in event payload")
	}

	message := formatMessage(event)

	post := map[string]string{
		"channel_id": channel,
		"message":    message,
	}

	// Thread replies: if the event carries a post_id, reply in-thread.
	if postID, _ := event.Payload["post_id"].(string); postID != "" {
		rootID, _ := event.Payload["root_id"].(string)
		if rootID == "" {
			rootID = postID // start a new thread under the triggering message
		}
		post["root_id"] = rootID
	}

	body, err := json.Marshal(post)
	if err != nil {
		return fmt.Errorf("mattermost: marshal post: %w", err)
	}

	postURL, err := url.JoinPath(p.cfg.URL, "/api/v4/posts")
	if err != nil {
		return fmt.Errorf("mattermost: build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", postURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mattermost request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mattermost api error %d: %s", resp.StatusCode, string(respBody))
	}

	p.log.Info("mattermost message sent", "channel", channel, "event_id", event.ID)
	return nil
}

func formatMessage(event plugin.Event) string {
	if summary, ok := event.Payload["summary"].(string); ok {
		return fmt.Sprintf("**[%s]** %s", event.Source, summary)
	}

	payload, err := json.MarshalIndent(event.Payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("**[%s/%s]** (failed to format payload)", event.Source, event.Type)
	}
	return fmt.Sprintf("**[%s/%s]**\n```json\n%s\n```", event.Source, event.Type, string(payload))
}
