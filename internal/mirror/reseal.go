package mirror

import (
	"context"
	"strings"
)

// Moves ssh keys sealed under the jwt secret and tokens stored plain under the sealing key
func (m *Monitor) ResealLegacy(ctx context.Context) error {
	if m.sealer == nil {
		return nil
	}
	n := 0
	seal := func(stored string) (string, bool) {
		if stored == "" || strings.HasPrefix(stored, sealedTokenPrefix) {
			return stored, false
		}
		return m.SealToken(stored), true
	}
	gits, err := m.store.ListAllGitRepositories(ctx)
	if err != nil {
		return err
	}
	for _, repo := range gits {
		cols := map[string]any{}
		state := parseGitMirrorState(repo.MirrorState)
		if state.SSHPrivateKey != "" {
			if _, err := m.sealer.Open(state.SSHPrivateKey); err != nil {
				if plain, err := m.openMirrorKey(state.SSHPrivateKey); err == nil {
					if sealed, err := m.sealer.Seal(string(plain)); err == nil {
						state.SSHPrivateKey = sealed
						cols["mirror_state"] = state.Encode()
					}
				} else {
					m.log.Warn("mirror: ssh key of %s/%s cannot be opened with any key, generate it again", repo.Namespace, repo.Name)
				}
			}
		}
		if cfg, err := ParseConfig(repo.MirrorConfig); err == nil && cfg.GetAuthToken() != "" {
			if sealed, changed := seal(cfg.GetAuthToken()); changed {
				cfg.AuthToken = &sealed
				if raw, err := EncodeConfig(cfg); err == nil {
					cols["mirror_config"] = raw
				}
			}
		}
		if len(cols) > 0 {
			if err := m.store.UpdateGitRepositoryColumns(ctx, repo.ID, cols); err != nil {
				return err
			}
			n++
		}
	}
	images, err := m.store.ListMirrorRepositories(ctx)
	if err != nil {
		return err
	}
	for _, repo := range images {
		cfg, err := ParseConfig(repo.MirrorConfig)
		if err != nil || cfg.GetAuthToken() == "" {
			continue
		}
		sealed, changed := seal(cfg.GetAuthToken())
		if !changed {
			continue
		}
		cfg.AuthToken = &sealed
		if repo.MirrorConfig, err = EncodeConfig(cfg); err != nil {
			return err
		}
		if err := m.store.UpdateRepository(ctx, repo); err != nil {
			return err
		}
		n++
	}
	arts, err := m.store.ListMirrorArtifactRepositories(ctx, MirrorArtifactTypes)
	if err != nil {
		return err
	}
	for _, repo := range arts {
		cfg, err := ParseConfig(repo.MirrorConfig)
		if err != nil || cfg.GetAuthToken() == "" {
			continue
		}
		sealed, changed := seal(cfg.GetAuthToken())
		if !changed {
			continue
		}
		cfg.AuthToken = &sealed
		if repo.MirrorConfig, err = EncodeConfig(cfg); err != nil {
			return err
		}
		if err := m.store.UpdateArtifactRepository(ctx, repo); err != nil {
			return err
		}
		n++
	}
	pushes, err := m.store.ListAllGitPushMirrors(ctx)
	if err != nil {
		return err
	}
	for _, pm := range pushes {
		sealed, changed := seal(pm.AuthToken)
		if !changed {
			continue
		}
		pm.AuthToken = sealed
		if err := m.store.UpdateGitPushMirror(ctx, pm); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		m.log.Info("mirror: resealed %d stored credentials under the sealing key", n)
	}
	return nil
}
