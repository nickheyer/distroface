package settings

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Output only fields no tier may write
var readOnlyPaths = []string{
	"auth.oidc.client_secret_set",
	"mail.password_set",
	"mail.incoming.password_set",
}

// Mail paths a lower tier may store, throttle, network and inbox stay system
var scopedMailPaths = []string{
	"mail.enabled", "mail.smtp_host", "mail.smtp_port", "mail.security", "mail.username",
	"mail.password", "mail.from_address", "mail.from_name", "mail.reply_to", "mail.skip_tls_verify",
}

// Workflow settings - repo or org scope
var scopedActionsPaths = []string{
	"git.actions.default_runs_on",
	"git.actions.run_on_mirror",
	"git.actions.artifact_retention_days",
	"git.actions.log_retention_days",
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// Paths each non system scope may store, prefixes cover subtrees
var scopeAllowed = map[v1.SettingsScopeType][]string{
	v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG: concat([]string{
		"acme.email",
		"acme.directory_url",
		"artifacts.max_file_size_mb",
		"artifacts.stale_upload_cleanup_hours",
		"artifacts.private_by_default",
		"artifacts.retention",
		"portals.isolated",
		"auth.anonymous_access",
		"git.lfs_enabled",
		"git.private_by_default",
		"git.default_branch",
		"git.max_repo_size_mb",
		"git.actions.enabled",
	}, scopedMailPaths, scopedActionsPaths),
	v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL: {
		"acme.email",
		"acme.directory_url",
		"tls.mtls_mode",
		"auth.anonymous_access",
	},
	v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO: concat(scopedMailPaths, scopedActionsPaths),
}

// Reports whether a mail leaf path may only come from the system tier
func systemOnlyMail(path string) bool {
	return path == "mail.allow_private_networks" || path == "mail.max_per_minute" || strings.HasPrefix(path, "mail.incoming.")
}

// Relay and sender travel together below system, a tier owns both or neither
func sealMail(eff *v1.Settings, chain []tierRow, prov map[string]v1.SettingsTier) {
	base := &v1.MailSettings{}
	if d := Defaults().GetMail(); d != nil {
		base = proto.Clone(d).(*v1.MailSettings)
	}
	for _, t := range chain {
		if t.tier == v1.SettingsTier_SETTINGS_TIER_SYSTEM && t.row.GetMail() != nil {
			overlay(base.ProtoReflect(), t.row.GetMail().ProtoReflect(), "mail", t.tier, prov)
		}
	}
	out := base
	for _, t := range chain {
		ms := t.row.GetMail()
		if t.tier == v1.SettingsTier_SETTINGS_TIER_SYSTEM || ms == nil {
			continue
		}
		for p, tier := range prov {
			if tier == t.tier && strings.HasPrefix(p, "mail.") {
				delete(prov, p)
			}
		}
		if ms.Enabled != nil {
			out.Enabled = ms.Enabled
			prov["mail.enabled"] = t.tier
		}
		if ms.SmtpHost == nil {
			continue
		}
		enabledTier, hadEnabled := prov["mail.enabled"]
		for p := range prov {
			if strings.HasPrefix(p, "mail.") && !systemOnlyMail(p) {
				delete(prov, p)
			}
		}
		if hadEnabled {
			prov["mail.enabled"] = enabledTier
		}
		own := proto.Clone(ms).(*v1.MailSettings)
		own.Enabled, own.AllowPrivateNetworks, own.MaxPerMinute, own.Incoming = out.Enabled, base.AllowPrivateNetworks, base.MaxPerMinute, base.Incoming
		for _, p := range setLeafPaths(ms) {
			if !systemOnlyMail("mail." + p) {
				prov["mail."+p] = t.tier
			}
		}
		out = own
	}
	eff.Mail = out
}

func pathCovered(path string, allowed []string) bool {
	for _, a := range allowed {
		if path == a || strings.HasPrefix(path, a+".") || strings.HasPrefix(a, path+".") {
			return true
		}
	}
	return false
}

// AllowedPath reports whether a scope may store the field
func AllowedPath(scope v1.SettingsScopeType, path string) bool {
	if pathCovered(path, readOnlyPaths) {
		return false
	}
	if scope == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM {
		return true
	}
	return pathCovered(path, scopeAllowed[scope])
}

// Copies populated fields of src onto dst recording the supplying tier
func overlay(dst, src protoreflect.Message, prefix string, tier v1.SettingsTier, prov map[string]v1.SettingsTier) {
	src.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		path := string(fd.Name())
		if prefix != "" {
			path = prefix + "." + path
		}
		if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && !fd.IsList() {
			overlay(dst.Mutable(fd).Message(), val.Message(), path, tier, prov)
			return true
		}
		dst.Set(fd, val)
		prov[path] = tier
		return true
	})
}

// Visits every leaf path of the schema regardless of presence
func walkLeaves(md protoreflect.MessageDescriptor, prefix string, fn func(path string)) {
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		path := string(fd.Name())
		if prefix != "" {
			path = prefix + "." + path
		}
		if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && !fd.IsList() {
			walkLeaves(fd.Message(), path, fn)
			continue
		}
		fn(path)
	}
}

// Populated leaf paths of one message
func setLeafPaths(m proto.Message) []string {
	var out []string
	var walk func(pm protoreflect.Message, prefix string)
	walk = func(pm protoreflect.Message, prefix string) {
		pm.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
			path := string(fd.Name())
			if prefix != "" {
				path = prefix + "." + path
			}
			if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && !fd.IsList() {
				walk(val.Message(), path)
				return true
			}
			out = append(out, path)
			return true
		})
	}
	walk(m.ProtoReflect(), "")
	sort.Strings(out)
	return out
}

// Resolves one dotted path to its terminal field within md
func descend(md protoreflect.MessageDescriptor, path string) ([]protoreflect.FieldDescriptor, error) {
	segs := strings.Split(path, ".")
	out := make([]protoreflect.FieldDescriptor, 0, len(segs))
	for i, seg := range segs {
		fd := md.Fields().ByName(protoreflect.Name(seg))
		if fd == nil {
			return nil, fmt.Errorf("unknown settings path %q", path)
		}
		out = append(out, fd)
		if i < len(segs)-1 {
			if fd.Kind() != protoreflect.MessageKind || fd.IsMap() || fd.IsList() {
				return nil, fmt.Errorf("settings path %q descends through a leaf", path)
			}
			md = fd.Message()
		}
	}
	return out, nil
}

// Sets masked paths present in patch onto stored, clears absent ones
func applyMask(stored, patch *v1.Settings, paths []string) error {
	for _, path := range paths {
		chain, err := descend(stored.ProtoReflect().Descriptor(), path)
		if err != nil {
			return err
		}
		sm := stored.ProtoReflect()
		pm := patch.ProtoReflect()
		for _, fd := range chain[:len(chain)-1] {
			sm = sm.Mutable(fd).Message()
			pm = pm.Get(fd).Message()
		}
		leaf := chain[len(chain)-1]
		if pm.IsValid() && pm.Has(leaf) {
			sm.Set(leaf, pm.Get(leaf))
		} else {
			sm.Clear(leaf)
		}
	}
	pruneEmpty(stored.ProtoReflect())
	return nil
}

// Drops submessages left with no populated fields
func pruneEmpty(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && !fd.IsList() {
			child := val.Message()
			pruneEmpty(child)
			empty := true
			child.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
				empty = false
				return false
			})
			if empty {
				m.Clear(fd)
			}
		}
		return true
	})
}

// Redact strips secret material from a settings tree in place
func Redact(s *v1.Settings) {
	if oidc := s.GetAuth().GetOidc(); oidc != nil {
		oidc.ClientSecretSet = oidc.ClientSecret != nil && *oidc.ClientSecret != ""
		oidc.ClientSecret = nil
	}
	if mail := s.GetMail(); mail != nil {
		mail.PasswordSet = mail.Password != nil && *mail.Password != ""
		mail.Password = nil
		if in := mail.GetIncoming(); in != nil {
			in.PasswordSet = in.Password != nil && *in.Password != ""
			in.Password = nil
		}
	}
}

// Provenance lists the supplying tier for every leaf of the schema
func provenanceList(prov map[string]v1.SettingsTier) []*v1.FieldProvenance {
	var out []*v1.FieldProvenance
	walkLeaves((&v1.Settings{}).ProtoReflect().Descriptor(), "", func(path string) {
		tier, ok := prov[path]
		if !ok {
			tier = v1.SettingsTier_SETTINGS_TIER_DEFAULT
		}
		out = append(out, &v1.FieldProvenance{Path: path, Tier: tier})
	})
	return out
}
