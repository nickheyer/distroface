package rbac

import (
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

func TestEveryActionsProcedureHasPolicy(t *testing.T) {
	svc := v1.File_distroface_v1_actions_proto.Services().ByName("ActionsService")
	if svc == nil {
		t.Fatal("ActionsService missing from the actions descriptor")
	}
	methods := svc.Methods()
	for i := range methods.Len() {
		proc := "/" + string(svc.FullName()) + "/" + string(methods.Get(i).Name())
		_, mapped := ProcedurePermissions[proc]
		if !mapped && !PublicProcedures[proc] && !IdentityProcedures[proc] && !AuthenticatedOnlyProcedures[proc] {
			t.Errorf("%s has no access policy, authorize denies it", proc)
		}
	}
	for _, proc := range []string{
		distrofacev1connect.ActionsServiceListWorkflowTemplatesProcedure,
		distrofacev1connect.ActionsServiceGetWorkflowTemplateProcedure,
	} {
		perm, ok := ProcedurePermissions[proc]
		if !ok || perm.Resource != ResourceGit || perm.Action != ActionRead || !perm.AnyGrant {
			t.Errorf("%s = %+v, want git/read with AnyGrant", proc, perm)
		}
	}
}

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
