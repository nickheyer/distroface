package portal

import (
	"context"
	"strings"
)

// Request-ready view of one org portal
type Portal struct {
	ID             string
	Name           string
	OrgName        string
	OrgDisplayName string
	MapUnqualified bool
	AllowPush      bool
	RequireAuth    bool
	TLS            bool
	rules          *pathMapper
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
