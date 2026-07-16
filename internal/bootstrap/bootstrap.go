package bootstrap

import (
	"context"
	"fmt"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Sentinel creator for orgs seeded without an owner
const createdByBootstrap = "bootstrap"

// Creates configured users then orgs never touching existing rows
func Run(ctx context.Context, cfg config.BootstrapConfig, store *db.Store, authManager *auth.Manager, log *logger.Logger) error {
	if err := seedUsers(ctx, cfg.Users, store, authManager, log); err != nil {
		return err
	}
	return seedOrgs(ctx, cfg.Orgs, store, log)
}

func seedUsers(ctx context.Context, users []config.BootstrapUser, store *db.Store, authManager *auth.Manager, log *logger.Logger) error {
	for _, u := range users {
		if u.Username == "" || u.Password == "" {
			return fmt.Errorf("bootstrap user requires username and password")
		}

		existing, err := store.GetUserByUsername(ctx, u.Username)
		if err != nil {
			return fmt.Errorf("bootstrap user %q: %w", u.Username, err)
		}
		if existing != nil {
			continue
		}

		user, err := authManager.CreateLocalUser(ctx, u.Username, u.Email, u.Password)
		if err != nil {
			return fmt.Errorf("bootstrap user %q: %w", u.Username, err)
		}

		roles := u.Roles
		if len(roles) == 0 {
			defaults, err := store.GetDefaultRoles(ctx)
			if err != nil {
				return fmt.Errorf("bootstrap user %q: %w", u.Username, err)
			}
			for _, r := range defaults {
				roles = append(roles, r.Name)
			}
		}
		for _, role := range roles {
			if err := store.AssignRole(ctx, user.ID, role, "local"); err != nil {
				return fmt.Errorf("bootstrap user %q role %q: %w", u.Username, role, err)
			}
		}

		log.Info("Bootstrap created user %q with roles %v", u.Username, roles)
	}
	return nil
}

func seedOrgs(ctx context.Context, orgs []config.BootstrapOrg, store *db.Store, log *logger.Logger) error {
	for _, o := range orgs {
		if o.Name == "" {
			return fmt.Errorf("bootstrap org requires name")
		}
		for _, m := range o.Members {
			if m.Username == "" {
				return fmt.Errorf("bootstrap org %q: member requires username", o.Name)
			}
			switch m.Role {
			case "", db.OrgRoleOwner, db.OrgRoleAdmin, db.OrgRoleMember:
			default:
				return fmt.Errorf("bootstrap org %q: invalid member role %q", o.Name, m.Role)
			}
		}

		org, err := store.GetOrganization(ctx, o.Name)
		if err != nil {
			return fmt.Errorf("bootstrap org %q: %w", o.Name, err)
		}
		if org == nil {
			// Org names must not shadow usernames
			user, err := store.GetUserByUsername(ctx, o.Name)
			if err != nil {
				return fmt.Errorf("bootstrap org %q: %w", o.Name, err)
			}
			if user != nil {
				return fmt.Errorf("bootstrap org %q: name taken by a user", o.Name)
			}

			displayName := o.DisplayName
			if displayName == "" {
				displayName = o.Name
			}
			org = &db.Organization{
				Name:        o.Name,
				DisplayName: displayName,
				Description: o.Description,
				CreatedBy:   resolveCreator(ctx, o.Members, store),
			}
			if err := store.CreateOrganization(ctx, org); err != nil {
				return fmt.Errorf("bootstrap org %q: %w", o.Name, err)
			}
			log.Info("Bootstrap created org %q", o.Name)
		}

		for _, m := range o.Members {
			user, err := store.GetUserByUsername(ctx, m.Username)
			if err != nil {
				return fmt.Errorf("bootstrap org %q member %q: %w", o.Name, m.Username, err)
			}
			if user == nil {
				// May appear later so retried on next startup
				log.Error("Bootstrap org %q: member %q not found, skipping", o.Name, m.Username)
				continue
			}

			existing, err := store.GetOrgMember(ctx, org.ID, user.ID)
			if err != nil {
				return fmt.Errorf("bootstrap org %q member %q: %w", o.Name, m.Username, err)
			}
			if existing != nil {
				continue
			}

			role := m.Role
			if role == "" {
				role = db.OrgRoleMember
			}
			if err := store.AddOrgMember(ctx, &db.OrgMember{
				OrgID:  org.ID,
				UserID: user.ID,
				Role:   role,
				Source: "local",
			}); err != nil {
				return fmt.Errorf("bootstrap org %q member %q: %w", o.Name, m.Username, err)
			}
			log.Info("Bootstrap added %q to org %q as %s", m.Username, o.Name, role)
		}
	}
	return nil
}

// First owner else first member else sentinel
func resolveCreator(ctx context.Context, members []config.BootstrapOrgMember, store *db.Store) string {
	pick := func(role string) string {
		for _, m := range members {
			if m.Role != role {
				continue
			}
			if user, err := store.GetUserByUsername(ctx, m.Username); err == nil && user != nil {
				return user.ID
			}
		}
		return ""
	}
	if id := pick(db.OrgRoleOwner); id != "" {
		return id
	}
	for _, m := range members {
		if user, err := store.GetUserByUsername(ctx, m.Username); err == nil && user != nil {
			return user.ID
		}
	}
	return createdByBootstrap
}
