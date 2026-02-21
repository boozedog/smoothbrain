package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
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
	bus         plugin.EventBus
	botID       string
	botName     string
	wsCancel    context.CancelFunc
	wsConnected atomic.Bool

	// Command dispatch.
	commands []plugin.CommandInfo
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{Timeout: 30 * time.Second},
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

func (p *Plugin) HealthCheck(ctx context.Context) plugin.HealthStatus {
	if !p.cfg.Listen {
		// Sink-only mode: ping the API to verify connectivity.
		u, err := url.JoinPath(p.cfg.URL, "/api/v4/system/ping")
		if err != nil {
			return plugin.HealthStatus{Status: plugin.StatusError, Message: "bad URL: " + err.Error()}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return plugin.HealthStatus{Status: plugin.StatusError, Message: err.Error()}
		}
		req.Header.Set("Authorization", "Bearer "+p.token)
		resp, err := p.client.Do(req)
		if err != nil {
			return plugin.HealthStatus{Status: plugin.StatusError, Message: "ping failed: " + err.Error()}
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return plugin.HealthStatus{Status: plugin.StatusError, Message: fmt.Sprintf("ping returned %d", resp.StatusCode)}
		}
		return plugin.HealthStatus{Status: plugin.StatusOK}
	}
	// Listen mode: check WebSocket connection state.
	if !p.wsConnected.Load() {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "websocket disconnected, reconnecting"}
	}
	return plugin.HealthStatus{Status: plugin.StatusOK}
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
		p.wsConnected.Store(false)

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
	p.wsConnected.Store(true)

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

// SetCommands provides the plugin with the list of routable commands.
func (p *Plugin) SetCommands(commands []plugin.CommandInfo) {
	p.commands = commands
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

	// Only respond to DMs or @mentions.
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

	// Strip @botname mention prefix.
	msg := post.Message
	msg = strings.ReplaceAll(msg, "@"+p.botName, "")
	msg = strings.TrimSpace(msg)

	// Parse subcommand (first word).
	subcmd, rest, _ := strings.Cut(msg, " ")
	subcmd = strings.ToLower(subcmd)
	rest = strings.TrimSpace(rest)

	// Handle "help" or unknown commands.
	if subcmd == "help" || !p.isKnownCommand(subcmd) {
		helpText := p.buildHelpText()
		if subcmd != "help" && subcmd != "" {
			helpText = fmt.Sprintf("Unknown command `%s`.\n\n%s", subcmd, helpText)
		}
		if err := p.sendPost(post.ChannelID, "", helpText); err != nil {
			p.log.Error("mattermost: send help", "error", err)
		}
		return
	}

	// Add thinking reaction for immediate feedback.
	if err := p.addReaction(post.ID, "hourglass_flowing_sand"); err != nil {
		p.log.Error("mattermost: add reaction", "error", err)
	}

	p.bus.Emit(plugin.Event{
		ID:        uuid.NewString(),
		Source:    "mattermost",
		Type:      subcmd,
		Timestamp: time.Now(),
		Payload: map[string]any{
			"channel":      post.ChannelID,
			"channel_id":   post.ChannelID,
			"post_id":      post.ID,
			"root_id":      post.RootID,
			"message":      rest,
			"user_id":      post.UserID,
			"sender_name":  ev.Data.SenderName,
			"channel_type": ev.Data.ChannelType,
		},
	})
}

func (p *Plugin) isKnownCommand(name string) bool {
	for _, c := range p.commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (p *Plugin) buildHelpText() string {
	var b strings.Builder
	b.WriteString("**Available commands:**\n")
	for _, c := range p.commands {
		if c.Description != "" {
			fmt.Fprintf(&b, "- `%s` — %s\n", c.Name, c.Description)
		} else {
			fmt.Fprintf(&b, "- `%s`\n", c.Name)
		}
	}
	b.WriteString("- `help` — Show this message\n")
	return b.String()
}

// sendPost posts a normal message to a channel, optionally in a thread.
func (p *Plugin) sendPost(channelID, rootID, text string) error {
	post := map[string]any{
		"channel_id": channelID,
		"message":    text,
	}
	if rootID != "" {
		post["root_id"] = rootID
	}

	body, err := json.Marshal(post)
	if err != nil {
		return err
	}

	u, err := url.JoinPath(p.cfg.URL, "/api/v4/posts")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post api error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// addReaction adds an emoji reaction to a post.
func (p *Plugin) addReaction(postID, emojiName string) error {
	body, err := json.Marshal(map[string]string{
		"user_id":    p.botID,
		"post_id":    postID,
		"emoji_name": emojiName,
	})
	if err != nil {
		return err
	}

	u, err := url.JoinPath(p.cfg.URL, "/api/v4/reactions")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reaction api error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// removeReaction removes an emoji reaction from a post.
func (p *Plugin) removeReaction(postID, emojiName string) error {
	u, err := url.JoinPath(p.cfg.URL, fmt.Sprintf("/api/v4/users/%s/posts/%s/reactions/%s", p.botID, postID, emojiName))
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), "DELETE", u, nil)
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
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove reaction api error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// --- Sink ---

func (p *Plugin) HandleEvent(ctx context.Context, event plugin.Event) error {
	channel, _ := event.Payload["channel"].(string)
	if channel == "" {
		return fmt.Errorf("mattermost: no channel in event payload")
	}

	message := formatMessage(event)

	post := map[string]any{
		"channel_id": channel,
		"message":    message,
	}

	// Thread replies: if the event carries a post_id, reply in-thread.
	if postID, _ := event.Payload["post_id"].(string); postID != "" {
		rootID, _ := event.Payload["root_id"].(string)
		if rootID == "" {
			rootID = postID
		}
		post["root_id"] = rootID
	}

	// Upload file attachment if present.
	if content, ok := event.Payload["file_content"].(string); ok && content != "" {
		filename, _ := event.Payload["file_name"].(string)
		if filename == "" {
			filename = "file.txt"
		}
		fileID, err := p.uploadFile(ctx, channel, filename, []byte(content))
		if err != nil {
			return fmt.Errorf("mattermost: upload file: %w", err)
		}
		post["file_ids"] = []string{fileID}
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

	// Remove thinking reaction now that the reply is posted.
	if postID, _ := event.Payload["post_id"].(string); postID != "" {
		if err := p.removeReaction(postID, "hourglass_flowing_sand"); err != nil {
			p.log.Debug("mattermost: remove reaction", "error", err)
		}
	}

	return nil
}

// uploadFile uploads a file to Mattermost and returns the file ID.
func (p *Plugin) uploadFile(ctx context.Context, channelID, filename string, content []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("channel_id", channelID); err != nil {
		return "", fmt.Errorf("write channel_id field: %w", err)
	}
	part, err := w.CreateFormFile("files", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", fmt.Errorf("write file content: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	uploadURL, err := url.JoinPath(p.cfg.URL, "/api/v4/files")
	if err != nil {
		return "", fmt.Errorf("build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		FileInfos []struct {
			ID string `json:"id"`
		} `json:"file_infos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if len(result.FileInfos) == 0 {
		return "", fmt.Errorf("no file info in upload response")
	}
	return result.FileInfos[0].ID, nil
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
