package mattermost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildWSURL_HTTPS(t *testing.T) {
	got := buildWSURL("https://mm.example.com")
	want := "wss://mm.example.com/api/v4/websocket"
	if got != want {
		t.Errorf("buildWSURL() = %q, want %q", got, want)
	}
}

func TestBuildWSURL_HTTP(t *testing.T) {
	got := buildWSURL("http://localhost:8065")
	want := "ws://localhost:8065/api/v4/websocket"
	if got != want {
		t.Errorf("buildWSURL() = %q, want %q", got, want)
	}
}

func TestBuildWSURL_TrailingSlash(t *testing.T) {
	got := buildWSURL("https://mm.example.com/")
	want := "wss://mm.example.com/api/v4/websocket"
	if got != want {
		t.Errorf("buildWSURL() = %q, want %q", got, want)
	}
}

func TestFormatMessage_Summary(t *testing.T) {
	ev := plugin.Event{
		Source:  "test-source",
		Payload: map[string]any{"summary": "something happened"},
	}
	got := formatMessage(ev)
	if !strings.Contains(got, "**[test-source]**") {
		t.Errorf("message = %q, want it to contain source", got)
	}
	if !strings.Contains(got, "something happened") {
		t.Errorf("message = %q, want it to contain summary", got)
	}
}

func TestFormatMessage_NoSummary(t *testing.T) {
	ev := plugin.Event{
		Source:  "test-source",
		Type:    "alert",
		Payload: map[string]any{"key": "value"},
	}
	got := formatMessage(ev)
	if !strings.Contains(got, "```json") {
		t.Errorf("message = %q, want it to contain JSON block", got)
	}
	if !strings.Contains(got, "test-source") {
		t.Errorf("message = %q, want it to contain source", got)
	}
}

func TestIsKnownCommand_Found(t *testing.T) {
	p := New(discardLogger())
	p.commands = []plugin.CommandInfo{
		{Name: "fetch", Description: "Fetch a URL"},
		{Name: "ask", Description: "Ask a question"},
	}
	if !p.isKnownCommand("fetch") {
		t.Error("isKnownCommand(\"fetch\") = false, want true")
	}
}

func TestIsKnownCommand_NotFound(t *testing.T) {
	p := New(discardLogger())
	p.commands = []plugin.CommandInfo{
		{Name: "fetch", Description: "Fetch a URL"},
	}
	if p.isKnownCommand("unknown") {
		t.Error("isKnownCommand(\"unknown\") = true, want false")
	}
}

func TestBuildHelpText(t *testing.T) {
	p := New(discardLogger())
	p.commands = []plugin.CommandInfo{
		{Name: "fetch", Description: "Fetch a URL"},
		{Name: "ask", Description: "Ask a question"},
	}
	got := p.buildHelpText()
	if !strings.Contains(got, "fetch") {
		t.Errorf("help text missing 'fetch': %q", got)
	}
	if !strings.Contains(got, "ask") {
		t.Errorf("help text missing 'ask': %q", got)
	}
	if !strings.Contains(got, "help") {
		t.Errorf("help text missing 'help': %q", got)
	}
}

func TestSetCommands(t *testing.T) {
	p := New(discardLogger())
	cmds := []plugin.CommandInfo{
		{Name: "cmd1", Description: "First"},
		{Name: "cmd2", Description: "Second"},
	}
	p.SetCommands(cmds)
	if len(p.commands) != 2 {
		t.Errorf("len(commands) = %d, want 2", len(p.commands))
	}
	if p.commands[0].Name != "cmd1" {
		t.Errorf("commands[0].Name = %q, want %q", p.commands[0].Name, "cmd1")
	}
}

// --- Test helpers ---

type captureBus struct {
	mu     sync.Mutex
	events []plugin.Event
}

func (b *captureBus) Emit(e plugin.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *captureBus) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

func (b *captureBus) get(i int) plugin.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.events[i]
}

func makeWSPostedMessage(userID, channelType, message string) []byte {
	post := map[string]any{
		"id":         "post123",
		"message":    message,
		"channel_id": "chan123",
		"user_id":    userID,
		"root_id":    "",
	}
	postJSON, _ := json.Marshal(post)
	ev := map[string]any{
		"event": "posted",
		"data": map[string]any{
			"post":         string(postJSON),
			"channel_type": channelType,
			"sender_name":  "testuser",
		},
		"broadcast": map[string]any{
			"channel_id": "chan123",
		},
	}
	data, _ := json.Marshal(ev)
	return data
}

func newTestWSPlugin(t *testing.T, handler http.HandlerFunc) (*Plugin, *captureBus) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"
	p.botID = "bot123"
	p.botName = "mybot"
	bus := &captureBus{}
	p.bus = bus
	p.commands = []plugin.CommandInfo{
		{Name: "ask", Description: "Ask a question"},
		{Name: "fetch", Description: "Fetch a URL"},
	}
	return p, bus
}

func acceptAllHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/posts") {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

// --- handleWSMessage tests ---

func TestHandleWSMessage_NonPostedEvent(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	ev := map[string]any{
		"event": "typing",
		"data":  map[string]any{},
	}
	data, _ := json.Marshal(ev)
	p.handleWSMessage(data)

	if bus.len() != 0 {
		t.Errorf("expected 0 events, got %d", bus.len())
	}
}

func TestHandleWSMessage_OwnBotMessage(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	// Message from the bot's own user ID
	data := makeWSPostedMessage("bot123", "D", "hello")
	p.handleWSMessage(data)

	if bus.len() != 0 {
		t.Errorf("expected 0 events for own bot message, got %d", bus.len())
	}
}

func TestHandleWSMessage_DM(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	data := makeWSPostedMessage("user456", "D", "ask how are you")
	p.handleWSMessage(data)

	if bus.len() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.len())
	}
	ev := bus.get(0)
	if ev.Source != "mattermost" {
		t.Errorf("Source = %q, want %q", ev.Source, "mattermost")
	}
}

func TestHandleWSMessage_MentionInChannel(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	data := makeWSPostedMessage("user456", "O", "@mybot ask what time is it")
	p.handleWSMessage(data)

	if bus.len() != 1 {
		t.Fatalf("expected 1 event for @mention, got %d", bus.len())
	}
}

func TestHandleWSMessage_NoMentionNoEmit(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	data := makeWSPostedMessage("user456", "O", "hello everyone")
	p.handleWSMessage(data)

	if bus.len() != 0 {
		t.Errorf("expected 0 events for non-mention in open channel, got %d", bus.len())
	}
}

func TestHandleWSMessage_InvalidJSON(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	p.handleWSMessage([]byte("not json at all {{{"))

	if bus.len() != 0 {
		t.Errorf("expected 0 events for invalid JSON, got %d", bus.len())
	}
}

func TestHandleWSMessage_CommandParsing(t *testing.T) {
	p, bus := newTestWSPlugin(t, acceptAllHandler)

	data := makeWSPostedMessage("user456", "D", "ask how are you")
	p.handleWSMessage(data)

	if bus.len() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.len())
	}
	ev := bus.get(0)
	if ev.Type != "ask" {
		t.Errorf("Type = %q, want %q", ev.Type, "ask")
	}
	msg, _ := ev.Payload["message"].(string)
	if msg != "how are you" {
		t.Errorf("message = %q, want %q", msg, "how are you")
	}
}

// --- HandleEvent (sink) tests ---

func TestHandleEvent_NoChannel(t *testing.T) {
	p := New(discardLogger())
	p.cfg.URL = "http://localhost"
	p.token = "test-token"

	ev := plugin.Event{
		Source:  "test",
		Type:    "alert",
		Payload: map[string]any{"summary": "something"},
	}

	err := p.HandleEvent(context.Background(), ev)
	if err == nil {
		t.Fatal("expected error for missing channel, got nil")
	}
	if !strings.Contains(err.Error(), "no channel") {
		t.Errorf("error = %q, want it to contain 'no channel'", err.Error())
	}
}

func TestHandleEvent_Success(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/posts") {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"

	ev := plugin.Event{
		ID:      "ev1",
		Source:  "test",
		Type:    "alert",
		Payload: map[string]any{"channel": "chan123", "summary": "hello"},
	}

	err := p.HandleEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if gotBody["channel_id"] != "chan123" {
		t.Errorf("channel_id = %v, want %q", gotBody["channel_id"], "chan123")
	}
}

func TestHandleEvent_ThreadReply(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/posts") {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"
	p.botID = "bot123"

	ev := plugin.Event{
		ID:     "ev1",
		Source: "test",
		Type:   "reply",
		Payload: map[string]any{
			"channel": "chan123",
			"post_id": "post456",
			"root_id": "root789",
			"summary": "reply text",
		},
	}

	err := p.HandleEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if gotBody["root_id"] != "root789" {
		t.Errorf("root_id = %v, want %q", gotBody["root_id"], "root789")
	}
}

func TestHandleEvent_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"

	ev := plugin.Event{
		Source:  "test",
		Payload: map[string]any{"channel": "chan123", "summary": "hello"},
	}

	err := p.HandleEvent(context.Background(), ev)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to contain '500'", err.Error())
	}
}

// --- sendPost tests ---

func TestSendPost_Success(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"

	err := p.sendPost("chan123", "", "hello world")
	if err != nil {
		t.Fatalf("sendPost() error = %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
	if gotBody["channel_id"] != "chan123" {
		t.Errorf("channel_id = %v, want %q", gotBody["channel_id"], "chan123")
	}
	if gotBody["message"] != "hello world" {
		t.Errorf("message = %v, want %q", gotBody["message"], "hello world")
	}
	if _, ok := gotBody["root_id"]; ok {
		t.Error("root_id should not be present when empty")
	}
}

func TestSendPost_WithRootID(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"

	err := p.sendPost("chan123", "root456", "threaded reply")
	if err != nil {
		t.Fatalf("sendPost() error = %v", err)
	}
	if gotBody["root_id"] != "root456" {
		t.Errorf("root_id = %v, want %q", gotBody["root_id"], "root456")
	}
}

func TestSendPost_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer ts.Close()

	p := New(discardLogger())
	p.cfg.URL = ts.URL
	p.token = "test-token"

	err := p.sendPost("chan123", "", "hello")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want it to contain '400'", err.Error())
	}
}

// --- Init test ---

func TestInit_ConfigParsing(t *testing.T) {
	p := New(discardLogger())
	cfg := `{"url":"https://mm.example.com","token":"mytoken","listen":true}`
	err := p.Init(json.RawMessage(cfg))
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if p.cfg.URL != "https://mm.example.com" {
		t.Errorf("URL = %q, want %q", p.cfg.URL, "https://mm.example.com")
	}
	if p.token != "mytoken" {
		t.Errorf("token = %q, want %q", p.token, "mytoken")
	}
	if !p.cfg.Listen {
		t.Error("Listen = false, want true")
	}
}
