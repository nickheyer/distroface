package agents

import (
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
)

func TestBotAvatarFollowsDefinitionUnlessUploaded(t *testing.T) {
	h := newHarness(t, nil)
	spec := AgentSpec{Slug: "painter", Name: "Painter", Kind: KindChat, AvatarURL: "https://example.com/a.png", Enabled: true}
	def := h.defineAgent(spec)
	u, err := h.agents.Identity(h.ctx, def)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if u.AvatarURL != spec.AvatarURL {
		t.Fatalf("avatar = %q, want %q", u.AvatarURL, spec.AvatarURL)
	}

	row, err := h.store.GetAgent(h.ctx, def.ID)
	if err != nil || row == nil {
		t.Fatalf("agent row: %v", err)
	}
	spec.AvatarURL = "/images/b.png"
	if err := h.agents.UpdateAgent(h.ctx, row, spec); err != nil {
		t.Fatalf("update: %v", err)
	}
	if u, _ = h.store.GetUserByID(h.ctx, u.ID); u.AvatarURL != "/images/b.png" {
		t.Fatalf("avatar after edit = %q, want /images/b.png", u.AvatarURL)
	}

	uploaded := "/api/avatars/" + u.ID
	if err := h.store.SetUserAvatar(h.ctx, &db.UserAvatar{UserID: u.ID, ContentType: "image/png", Data: []byte{1}, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	u.AvatarURL = uploaded
	if err := h.store.UpdateUser(h.ctx, u); err != nil {
		t.Fatalf("user: %v", err)
	}
	spec.AvatarURL = "https://example.com/c.png"
	if err := h.agents.UpdateAgent(h.ctx, row, spec); err != nil {
		t.Fatalf("update: %v", err)
	}
	if u, _ = h.store.GetUserByID(h.ctx, u.ID); u.AvatarURL != uploaded {
		t.Fatalf("avatar after upload = %q, want %q", u.AvatarURL, uploaded)
	}

	if err := h.agents.UpdateAgent(h.ctx, row, AgentSpec{Slug: "painter", Name: "Painter", Kind: KindChat, AvatarURL: "javascript:alert(1)"}); err == nil {
		t.Fatal("expected an invalid avatar url to be rejected")
	}
}

func TestParseAgentFileAvatar(t *testing.T) {
	d, err := ParseAgentFile(".distroface/agents/helper.md", "---\nname: Helper\navatar: https://example.com/h.png\n---\nBe helpful.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.AvatarURL != "https://example.com/h.png" {
		t.Fatalf("avatar = %q", d.AvatarURL)
	}
	if _, err := ParseAgentFile(".distroface/agents/bad.md", "---\navatar: data:image/png;base64,AAAA\n---\nBe bad."); err == nil {
		t.Fatal("expected an invalid avatar to error")
	}
}
