package settings

import (
	"context"
	"fmt"
	"sync"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Store persists one protojson settings row per scope
type Store interface {
	GetSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID string) (string, bool, error)
	SetSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID, value string) error
	DeleteSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID string) error
	GetPortalOrgID(ctx context.Context, portalID string) (string, error)
}

type scopeKey struct {
	scope v1.SettingsScopeType
	id    string
}

// Resolver merges stored tiers over defaults under file pins
type Resolver struct {
	store     Store
	overrides *v1.Settings
	locked    []string

	mu        sync.RWMutex
	rows      map[scopeKey]*v1.Settings
	portalOrg map[string]string
	subs      []func()
}

func NewResolver(store Store, fileOverrides *v1.Settings) *Resolver {
	r := &Resolver{
		store:     store,
		overrides: fileOverrides,
		rows:      map[scopeKey]*v1.Settings{},
		portalOrg: map[string]string{},
	}
	if fileOverrides != nil {
		r.locked = setLeafPaths(fileOverrides)
	}
	return r
}

// LockedPaths lists fields pinned by the config file
func (r *Resolver) LockedPaths() []string {
	return r.locked
}

// Subscribe registers a callback fired after any settings write
func (r *Resolver) Subscribe(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = append(r.subs, fn)
}

// Invalidate drops all cached rows
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.rows = map[scopeKey]*v1.Settings{}
	r.portalOrg = map[string]string{}
	r.mu.Unlock()
}

func (r *Resolver) notify() {
	r.mu.RLock()
	subs := append([]func(){}, r.subs...)
	r.mu.RUnlock()
	for _, fn := range subs {
		fn()
	}
}

// SeedSystem writes the file seed once when no system row exists,
// reports whether the seed applied
func (r *Resolver) SeedSystem(ctx context.Context, seed *v1.Settings) (bool, error) {
	if seed == nil {
		return false, nil
	}
	_, found, err := r.store.GetSettingsValue(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if err != nil {
		return false, err
	}
	if found {
		return false, nil
	}
	if err := r.save(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", seed); err != nil {
		return false, err
	}
	return true, nil
}

// Stored returns the raw row for one scope, empty when absent
func (r *Resolver) Stored(ctx context.Context, scope v1.SettingsScopeType, scopeID string) (*v1.Settings, error) {
	key := scopeKey{scope, scopeID}
	r.mu.RLock()
	cached, ok := r.rows[key]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}

	raw, found, err := r.store.GetSettingsValue(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	row := &v1.Settings{}
	if found {
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(raw), row); err != nil {
			return nil, fmt.Errorf("corrupt settings row %v/%s: %w", scope, scopeID, err)
		}
	}
	r.mu.Lock()
	r.rows[key] = row
	r.mu.Unlock()
	return row, nil
}

func (r *Resolver) orgForPortal(ctx context.Context, portalID string) (string, error) {
	r.mu.RLock()
	orgID, ok := r.portalOrg[portalID]
	r.mu.RUnlock()
	if ok {
		return orgID, nil
	}
	orgID, err := r.store.GetPortalOrgID(ctx, portalID)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.portalOrg[portalID] = orgID
	r.mu.Unlock()
	return orgID, nil
}

// Effective resolves the full chain for a scope with provenance
func (r *Resolver) Effective(ctx context.Context, scope v1.SettingsScopeType, scopeID string) (*v1.Settings, []*v1.FieldProvenance, error) {
	type tierRow struct {
		row  *v1.Settings
		tier v1.SettingsTier
	}
	chain := []tierRow{}

	system, err := r.Stored(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if err != nil {
		return nil, nil, err
	}
	chain = append(chain, tierRow{system, v1.SettingsTier_SETTINGS_TIER_SYSTEM})

	switch scope {
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG:
		org, err := r.Stored(ctx, scope, scopeID)
		if err != nil {
			return nil, nil, err
		}
		chain = append(chain, tierRow{org, v1.SettingsTier_SETTINGS_TIER_ORG})
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL:
		orgID, err := r.orgForPortal(ctx, scopeID)
		if err != nil {
			return nil, nil, err
		}
		org, err := r.Stored(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, orgID)
		if err != nil {
			return nil, nil, err
		}
		portal, err := r.Stored(ctx, scope, scopeID)
		if err != nil {
			return nil, nil, err
		}
		chain = append(chain,
			tierRow{org, v1.SettingsTier_SETTINGS_TIER_ORG},
			tierRow{portal, v1.SettingsTier_SETTINGS_TIER_PORTAL})
	}

	eff := proto.Clone(Defaults()).(*v1.Settings)
	prov := map[string]v1.SettingsTier{}
	for _, t := range chain {
		overlay(eff.ProtoReflect(), t.row.ProtoReflect(), "", t.tier, prov)
	}
	if r.overrides != nil {
		overlay(eff.ProtoReflect(), r.overrides.ProtoReflect(), "", v1.SettingsTier_SETTINGS_TIER_FILE, prov)
	}
	return eff, provenanceList(prov), nil
}

// System is the common case shorthand
func (r *Resolver) System(ctx context.Context) *v1.Settings {
	eff, _, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if err != nil {
		return Defaults()
	}
	return eff
}

// Org resolves org tier falling back to system on lookup failure
func (r *Resolver) Org(ctx context.Context, orgID string) *v1.Settings {
	eff, _, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, orgID)
	if err != nil {
		return r.System(ctx)
	}
	return eff
}

// Portal resolves portal tier falling back to system on lookup failure
func (r *Resolver) Portal(ctx context.Context, portalID string) *v1.Settings {
	eff, _, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, portalID)
	if err != nil {
		return r.System(ctx)
	}
	return eff
}

// Update applies a field masked patch to one scope and persists it
func (r *Resolver) Update(ctx context.Context, scope v1.SettingsScopeType, scopeID string, patch *v1.Settings, paths []string) (*v1.Settings, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("update mask required")
	}
	for _, path := range paths {
		if _, err := descend((&v1.Settings{}).ProtoReflect().Descriptor(), path); err != nil {
			return nil, err
		}
		if !AllowedPath(scope, path) {
			return nil, fmt.Errorf("path %q not writable at this scope", path)
		}
		if pathCovered(path, r.locked) {
			return nil, fmt.Errorf("path %q is pinned by the config file", path)
		}
	}

	current, err := r.Stored(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	stored := proto.Clone(current).(*v1.Settings)
	if err := applyMask(stored, patch, paths); err != nil {
		return nil, err
	}
	if err := r.save(ctx, scope, scopeID, stored); err != nil {
		return nil, err
	}
	r.notify()
	return stored, nil
}

// DeleteScope removes a scope's row on org or portal deletion
func (r *Resolver) DeleteScope(ctx context.Context, scope v1.SettingsScopeType, scopeID string) error {
	if err := r.store.DeleteSettingsValue(ctx, scope, scopeID); err != nil {
		return err
	}
	r.Invalidate()
	r.notify()
	return nil
}

func (r *Resolver) save(ctx context.Context, scope v1.SettingsScopeType, scopeID string, row *v1.Settings) error {
	raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(row)
	if err != nil {
		return err
	}
	if err := r.store.SetSettingsValue(ctx, scope, scopeID, string(raw)); err != nil {
		return err
	}
	r.mu.Lock()
	r.rows[scopeKey{scope, scopeID}] = row
	r.mu.Unlock()
	return nil
}
