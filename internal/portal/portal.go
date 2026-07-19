package portal

import (
	"context"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Request-ready view of one org portal
type Portal struct {
	ID              string
	Name            string
	OrgID           string
	OrgName         string
	OrgDisplayName  string
	MapUnqualified  bool
	AllowPush       bool
	RequireAuth     bool
	TLS             bool
	CertSource      v1.CertSource
	CatchAll        bool
	Isolated        bool
	HidePrimaryLink bool
	rules           *pathMapper
}

// Resolves a requested repo name to its canonical path, custom rules run
// first (results must land in the org namespace), then unqualified names
// get the org prefix when map_unqualified is set
func (p *Portal) MapName(name string) string {
	if p.rules != nil {
		if mapped := p.rules.MapName(name); mapped != name {
			if strings.HasPrefix(mapped, p.OrgName+"/") {
				return mapped
			}
			return name
		}
	}
	if p.MapUnqualified && !strings.Contains(name, "/") {
		return p.OrgName + "/" + name
	}
	return name
}

// InScope reports whether a canonical repo path may serve here
func (p *Portal) InScope(name string) bool {
	if !p.Isolated {
		return true
	}
	return strings.HasPrefix(name, p.OrgName+"/")
}

// ForeignRef reports an isolated portal hiding this namespace
func ForeignRef(ctx context.Context, namespace string) bool {
	p := FromContext(ctx)
	return p != nil && p.Isolated && namespace != p.OrgName
}

type ctxKey struct{}

func WithPortal(ctx context.Context, p *Portal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// Portal serving the request, nil outside portal traffic
func FromContext(ctx context.Context) *Portal {
	p, _ := ctx.Value(ctxKey{}).(*Portal)
	return p
}

// Forces the portal org namespace on portal traffic
func ScopeNamespace(ctx context.Context, namespace string) string {
	if p := FromContext(ctx); p != nil {
		return p.OrgName
	}
	return namespace
}

// Unqualified repo refs resolve like the data plane
func ScopeRepoRef(ctx context.Context, namespace, name string) (string, string) {
	p := FromContext(ctx)
	if p == nil || namespace != "" {
		return namespace, name
	}
	if mapped := p.MapName(name); strings.HasPrefix(mapped, p.OrgName+"/") {
		return p.OrgName, strings.TrimPrefix(mapped, p.OrgName+"/")
	}
	return namespace, name
}
