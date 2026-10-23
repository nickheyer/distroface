package migrations

import (
	"path/filepath"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSplitAgentSessionTokens(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "m.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&db.AgentSession{}, &db.AgentSessionEvent{}); err != nil {
		t.Fatal(err)
	}
	// Old rows summed every prompt, three turns of a growing transcript
	old := &db.AgentSession{ID: "s1", RepoID: "r", Turns: 3, InputTokens: 3750, OutputTokens: 200}
	if err := gdb.Create(old).Error; err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{
		`{"input_tokens":1000,"output_tokens":50}`,
		`{"input_tokens":1250,"output_tokens":50}`,
		`{"input_tokens":1500,"output_tokens":100}`,
	} {
		ev := &db.AgentSessionEvent{SessionID: "s1", Seq: int64(i + 1), Kind: "usage", Data: data}
		if err := gdb.Create(ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := splitAgentSessionTokens(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
	var got db.AgentSession
	if err := gdb.First(&got, "id = ?", "s1").Error; err != nil {
		t.Fatal(err)
	}
	if got.PromptTokens != 3750 || got.InputTokens != 1400 || got.OutputTokens != 200 || got.ContextTokens != 1600 {
		t.Fatalf("prompt=%d in=%d out=%d context=%d", got.PromptTokens, got.InputTokens, got.OutputTokens, got.ContextTokens)
	}
}
