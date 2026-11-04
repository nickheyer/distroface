package agents

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Transcript event kinds
const (
	EventStatus     = "status"
	EventSystem     = "system"
	EventUser       = "user"
	EventAssistant  = "assistant"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventApproval   = "approval"
	EventError      = "error"
	EventResult     = "result"
	EventUsage      = "usage"
)

// Fan out of live events to streaming watchers
type eventHub struct {
	mu   sync.Mutex
	subs map[string]map[chan *db.AgentSessionEvent]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[string]map[chan *db.AgentSessionEvent]struct{}{}}
}

// Subscribes to one session, the channel drops events when the watcher lags
func (h *eventHub) subscribe(sessionID string) (<-chan *db.AgentSessionEvent, func()) {
	ch := make(chan *db.AgentSessionEvent, 128)
	h.mu.Lock()
	if h.subs[sessionID] == nil {
		h.subs[sessionID] = map[chan *db.AgentSessionEvent]struct{}{}
	}
	h.subs[sessionID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if set := h.subs[sessionID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subs, sessionID)
			}
		}
		h.mu.Unlock()
	}
}

func (h *eventHub) publish(ev *db.AgentSessionEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[ev.SessionID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe exposes live events of one session to the rpc layer
func (m *Manager) Subscribe(sessionID string) (<-chan *db.AgentSessionEvent, func()) {
	return m.hub.subscribe(sessionID)
}

// Persists and publishes one transcript event
func (m *Manager) record(ctx context.Context, sess *db.AgentSession, kind, text, tool, callID string, data any) *db.AgentSessionEvent {
	raw := "{}"
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			raw = string(b)
		}
	}
	ev := &db.AgentSessionEvent{SessionID: sess.ID, Kind: kind, Text: text, Tool: tool, ToolCallID: callID, Data: raw}
	if err := m.store.AppendAgentSessionEvent(context.WithoutCancel(ctx), ev); err != nil {
		m.log.Error("agents: record %s event on %s: %v", kind, sess.ID, err)
		return ev
	}
	sess.EventCount = ev.Seq
	m.hub.publish(ev)
	return ev
}

// ProtoEventKind maps a stored kind onto the enum
func ProtoEventKind(kind string) v1.AgentEventKind {
	switch kind {
	case EventStatus:
		return v1.AgentEventKind_AGENT_EVENT_KIND_STATUS
	case EventSystem:
		return v1.AgentEventKind_AGENT_EVENT_KIND_SYSTEM
	case EventUser:
		return v1.AgentEventKind_AGENT_EVENT_KIND_USER
	case EventAssistant:
		return v1.AgentEventKind_AGENT_EVENT_KIND_ASSISTANT
	case EventToolCall:
		return v1.AgentEventKind_AGENT_EVENT_KIND_TOOL_CALL
	case EventToolResult:
		return v1.AgentEventKind_AGENT_EVENT_KIND_TOOL_RESULT
	case EventApproval:
		return v1.AgentEventKind_AGENT_EVENT_KIND_APPROVAL
	case EventError:
		return v1.AgentEventKind_AGENT_EVENT_KIND_ERROR
	case EventResult:
		return v1.AgentEventKind_AGENT_EVENT_KIND_RESULT
	case EventUsage:
		return v1.AgentEventKind_AGENT_EVENT_KIND_USAGE
	}
	return v1.AgentEventKind_AGENT_EVENT_KIND_UNSPECIFIED
}
