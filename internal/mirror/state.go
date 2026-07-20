package mirror

import (
	"encoding/json"
	"time"
)

// Per repo sync bookkeeping persisted between sweeps
type SyncState struct {
	// Conditional request cursor for the release listing
	ListETag string `json:"list_etag,omitempty"`
	// Consecutive failed syncs, drives exponential backoff
	Failures int `json:"failures,omitempty"`
	// No syncs before this instant
	CooldownUntil time.Time `json:"cooldown_until,omitzero"`
	// Cooldown came from an upstream rate limit answer
	RateLimited bool `json:"rate_limited,omitempty"`
}

func ParseState(raw string) SyncState {
	var st SyncState
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s SyncState) Encode() string {
	if s == (SyncState{}) {
		return ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// CoolingDown reports whether syncs must still wait
func (s SyncState) CoolingDown(now time.Time) bool {
	return !s.CooldownUntil.IsZero() && now.Before(s.CooldownUntil)
}
