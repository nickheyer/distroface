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
// first and may target any namespace, then unqualified names get the org
// prefix when map_unqualified is set
func (p *Portal) MapName(name string) string {
	if p.rules != nil {
		if mapped := p.rules.MapName(name); mapped != name {
			return mapped
		}
	}
	if p.MapUnqualified && !strings.Contains(name, "/") {
		return p.OrgName + "/" + name
	}
	return name
}

// InScope reports whether a canonical repo path may serve here
func (p *Portal) InScope(name string) bool {
	if !p.Isolated || strings.HasPrefix(name, p.OrgName+"/") {
		return true
	}
	ns, _, ok := strings.Cut(name, "/")
	return ok && p.ruleNamespace(ns)
}

// Admin rules extend an isolated portal into their target namespaces
func (p *Portal) ruleNamespace(namespace string) bool {
	return p.rules != nil && p.rules.namespaces[namespace]
}

// ForeignRef reports an isolated portal hiding this namespace
func ForeignRef(ctx context.Context, namespace string) bool {
	p := FromContext(ctx)
	return p != nil && p.Isolated && namespace != p.OrgName && !p.ruleNamespace(namespace)
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
	mapped := p.MapName(name)
	if ns, base, ok := strings.Cut(mapped, "/"); ok {
		return ns, base
	}
	return namespace, mapped
}
