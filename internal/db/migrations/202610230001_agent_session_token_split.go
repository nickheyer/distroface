package migrations

import (
	"encoding/json"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610230001",
		name:    "agent_session_token_split",
		migrate: splitAgentSessionTokens,
	})
}

// Old input totals summed every prompt, which is the billed figure
func splitAgentSessionTokens(tx *gorm.DB, log *logger.Logger) error {
	if !tx.Migrator().HasTable(&db.AgentSession{}) {
		return nil
	}
	if err := tx.Model(&db.AgentSession{}).Where("prompt_tokens = 0 AND input_tokens > 0").UpdateColumn("prompt_tokens", gorm.Expr("input_tokens")).Error; err != nil {
		return err
	}
	var sessions []db.AgentSession
	if err := tx.Select("id").Where("input_tokens > 0").Find(&sessions).Error; err != nil {
		return err
	}
	fixed := 0
	for _, s := range sessions {
		var events []db.AgentSessionEvent
		if err := tx.Select("data").Where("session_id = ? AND kind = ?", s.ID, "usage").Order("seq ASC").Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			continue
		}
		var input, output, context int64
		for _, ev := range events {
			var u struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
			}
			if json.Unmarshal([]byte(ev.Data), &u) != nil {
				continue
			}
			if fresh := u.Input - context; fresh > 0 {
				input += fresh
			}
			output += u.Output
			context = u.Input + u.Output
		}
		cols := map[string]any{"input_tokens": input, "output_tokens": output, "context_tokens": context}
		if err := tx.Model(&db.AgentSession{}).Where("id = ?", s.ID).UpdateColumns(cols).Error; err != nil {
			return err
		}
		fixed++
	}
	if fixed > 0 {
		log.Info("recounted transcript tokens of %d agent sessions", fixed)
	}
	return nil
}
