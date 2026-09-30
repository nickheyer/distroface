package rbac

import (
	"testing"

	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

func TestProcedureTiersDisjoint(t *testing.T) {
	tiers := map[string]map[string]bool{
		"public":   PublicProcedures,
		"identity": IdentityProcedures,
		"account":  AuthenticatedOnlyProcedures,
	}
	seen := map[string]string{}
	for tier, procs := range tiers {
		for proc := range procs {
			if prev, ok := seen[proc]; ok {
				t.Errorf("%s listed in both %s and %s", proc, prev, tier)
			}
			seen[proc] = tier
			if _, ok := ProcedurePermissions[proc]; ok {
				t.Errorf("%s listed in %s and in ProcedurePermissions", proc, tier)
			}
		}
	}
}

func TestRepositoryBrowsingNeedsReadGrant(t *testing.T) {
	browse := []string{
		distrofacev1connect.RepositoryServiceListRepositoriesProcedure,
		distrofacev1connect.RepositoryServiceGetRepositoryProcedure,
		distrofacev1connect.RepositoryServiceListTagsProcedure,
		distrofacev1connect.RepositoryServiceResolveTagProcedure,
	}
	for _, proc := range browse {
		if PublicProcedures[proc] {
			t.Errorf("%s must not bypass rbac", proc)
		}
		perm, ok := ProcedurePermissions[proc]
		if !ok || perm.Resource != ResourceRepositories || perm.Action != ActionRead || !perm.AnyGrant {
			t.Errorf("%s = %+v, want repositories/read with AnyGrant", proc, perm)
		}
	}
}
