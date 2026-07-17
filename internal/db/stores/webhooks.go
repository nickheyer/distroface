package stores

import (
	"context"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── Webhook operations ────────────────────────────────────────────────────

func (s *Store) CreateWebhook(ctx context.Context, webhook *db.Webhook) error {
	if webhook.ID == "" {
		webhook.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(webhook).Error
}

func (s *Store) GetWebhook(ctx context.Context, id string) (*db.Webhook, error) {
	var webhook db.Webhook
	err := s.db.WithContext(ctx).First(&webhook, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &webhook, nil
}

func (s *Store) ListWebhooksByRepo(ctx context.Context, repoID string, limit, offset int) ([]*db.Webhook, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Webhook{}).Where("repo_id = ? AND scope = ?", repoID, db.WebhookScopeRepository)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var webhooks []*db.Webhook
	err := s.db.WithContext(ctx).Where("repo_id = ? AND scope = ?", repoID, db.WebhookScopeRepository).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&webhooks).Error
	return webhooks, total, err
}

func (s *Store) ListWebhooksByOrg(ctx context.Context, orgID string, limit, offset int) ([]*db.Webhook, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Webhook{}).Where("org_id = ? AND scope = ?", orgID, db.WebhookScopeOrganization)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var webhooks []*db.Webhook
	err := s.db.WithContext(ctx).Where("org_id = ? AND scope = ?", orgID, db.WebhookScopeOrganization).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&webhooks).Error
	return webhooks, total, err
}

func (s *Store) UpdateWebhook(ctx context.Context, webhook *db.Webhook) error {
	return s.db.WithContext(ctx).Save(webhook).Error
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.Webhook{}, "id = ?", id).Error
}

// Returns active webhooks for a repo
func (s *Store) GetActiveWebhooksForRepo(ctx context.Context, namespace, name string) ([]*db.Webhook, error) {
	var webhooks []*db.Webhook

	// Get the repo
	repo, err := s.GetRepository(ctx, namespace, name)
	if err != nil || repo == nil {
		return nil, err
	}

	// Repo-scoped webhooks
	err = s.db.WithContext(ctx).Where("repo_id = ? AND scope = ? AND active = ?", repo.ID, db.WebhookScopeRepository, true).Find(&webhooks).Error
	if err != nil {
		return nil, err
	}

	// Org-scoped webhooks: find org by namespace
	org, err := s.GetOrganization(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if org != nil {
		var orgWebhooks []*db.Webhook
		err = s.db.WithContext(ctx).Where("org_id = ? AND scope = ? AND active = ?", org.ID, db.WebhookScopeOrganization, true).Find(&orgWebhooks).Error
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, orgWebhooks...)
	}

	return webhooks, nil
}

// ── WebhookDelivery operations ────────────────────────────────────────────

func (s *Store) CreateWebhookDelivery(ctx context.Context, delivery *db.WebhookDelivery) error {
	if delivery.ID == "" {
		delivery.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(delivery).Error
}

func (s *Store) ListWebhookDeliveries(ctx context.Context, webhookID string, limit, offset int) ([]*db.WebhookDelivery, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.WebhookDelivery{}).Where("webhook_id = ?", webhookID)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var deliveries []*db.WebhookDelivery
	err := s.db.WithContext(ctx).Where("webhook_id = ?", webhookID).
		Order("delivered_at DESC").Limit(limit).Offset(offset).Find(&deliveries).Error
	return deliveries, total, err
}

func (s *Store) GetWebhookDelivery(ctx context.Context, id string) (*db.WebhookDelivery, error) {
	var delivery db.WebhookDelivery
	err := s.db.WithContext(ctx).First(&delivery, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &delivery, nil
}
