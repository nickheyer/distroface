package forge

import (
	"encoding/json"

	"github.com/nickheyer/distroface/internal/audit"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(raw string, v any) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), v)
}

// Audit row for a forge action by an actor
func auditEvent(actor Actor, action, detail string) audit.Event {
	ev := audit.Event{Action: action, Resource: "git", Outcome: audit.OutcomeSuccess, Detail: detail}
	if actor.User != nil {
		ev.Actor, ev.ActorID = actor.User.Username, actor.User.ID
	}
	return ev
}
