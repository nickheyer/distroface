package runner

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	neturl "net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Inputs of actions/checkout the runner honors
type checkoutInput struct {
	Repository         string
	Ref                string
	Commit             string
	Token              string
	Path               string
	Clean              bool
	Filter             string
	SparseCheckout     []string
	SparseCone         bool
	FetchDepth         int
	FetchTags          bool
	LFS                bool
	Submodules         string
	PersistCredentials bool
	SetSafeDirectory   bool
	ServerURL          string
}

// Resolves inputs the way actions/checkout does
func (j *job) checkoutInput(with map[string]string) (checkoutInput, error) {
	get := func(k string) string { return strings.TrimSpace(with[k]) }
	boolOr := func(k string, def bool) bool {
		v := strings.ToLower(get(k))
		if v == "" {
			return def
		}
		return v == "true"
	}
	in := checkoutInput{
		Repository: get("repository"), Ref: get("ref"), Token: with["token"], Path: get("path"),
		Clean: boolOr("clean", true), Filter: get("filter"), SparseCheckout: splitLines(with["sparse-checkout"]),
		SparseCone: boolOr("sparse-checkout-cone-mode", true), FetchDepth: 1, FetchTags: boolOr("fetch-tags", false),
		LFS: boolOr("lfs", false), Submodules: strings.ToLower(get("submodules")), PersistCredentials: boolOr("persist-credentials", true),
		SetSafeDirectory: boolOr("set-safe-directory", true), ServerURL: strings.TrimRight(get("github-server-url"), "/"),
	}
	if in.Repository == "" {
		in.Repository = j.task.RepoFullName
	}
	if v := get("fetch-depth"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return in, fmt.Errorf("fetch-depth %q is not a number", v)
		}
		in.FetchDepth = n
	}
	if in.Token == "" {
		in.Token = j.task.Token
	}
	switch in.Submodules {
	case "", "false":
		in.Submodules = ""
	case "true", "recursive":
	default:
		return in, fmt.Errorf("submodules must be true, false, or recursive")
	}
	// The workflow repository checks out the commit that triggered the run
	if in.Ref == "" && in.Repository == j.task.RepoFullName {
		in.Ref, in.Commit = stringify(j.github["ref"]), stringify(j.github["sha"])
		if in.Commit != "" && in.Ref != "" && !strings.HasPrefix(in.Ref, "refs/") {
			in.Ref = "refs/heads/" + in.Ref
		}
	}
	if shaPattern.MatchString(in.Ref) {
		in.Commit, in.Ref = strings.ToLower(in.Ref), ""
	}
	return in, nil
}

// Refspecs actions/checkout fetches for a ref and commit
func refSpecs(ref, commit string) []string {
	upper := strings.ToUpper(ref)
	if commit != "" {
		switch {
		case strings.HasPrefix(upper, "REFS/HEADS/"):
			return []string{"+" + commit + ":refs/remotes/origin/" + ref[len("refs/heads/"):]}
		case strings.HasPrefix(upper, "REFS/PULL/"):
			return []string{"+" + commit + ":refs/remotes/pull/" + ref[len("refs/pull/"):]}
		case strings.HasPrefix(upper, "REFS/TAGS/"):
			return []string{"+" + commit + ":" + ref}
		}
		return []string{commit}
	}
	switch {
	case ref == "":
		return []string{"HEAD"}
	case !strings.HasPrefix(upper, "REFS/"):
		return []string{"+refs/heads/" + ref + "*:refs/remotes/origin/" + ref + "*", "+refs/tags/" + ref + "*:refs/tags/" + ref + "*"}
	case strings.HasPrefix(upper, "REFS/HEADS/"):
		return []string{"+" + ref + ":refs/remotes/origin/" + ref[len("refs/heads/"):]}
	case strings.HasPrefix(upper, "REFS/PULL/"):
		return []string{"+" + ref + ":refs/remotes/pull/" + ref[len("refs/pull/"):]}
	case strings.HasPrefix(upper, "REFS/TAGS/"):
		return []string{"+" + ref + ":" + ref}
	}
	return []string{ref}
}

// Refspecs of a full history fetch
func allHistoryRefSpecs(ref, commit string) []string {
	out := []string{"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"}
	if strings.HasPrefix(strings.ToUpper(ref), "REFS/PULL/") {
		branch := ref[len("refs/pull/"):]
		out = append(out, "+"+firstNonEmpty(commit, ref)+":refs/remotes/pull/"+branch)
	}
	return out
}

// Shell lines that check out the fetched ref, mirroring getCheckoutInfo
func checkoutLines(ref, commit string) string {
	upper := strings.ToUpper(ref)
	switch {
	case ref == "" && commit != "":
		return "git checkout -q --force " + shellQuote(commit) + "\n"
	case ref == "":
		return "git checkout -q --force FETCH_HEAD\n"
	case strings.HasPrefix(upper, "REFS/HEADS/"):
		branch := ref[len("refs/heads/"):]
		return "git checkout -q --force -B " + shellQuote(branch) + " " + shellQuote("refs/remotes/origin/"+branch) + "\n"
	case strings.HasPrefix(upper, "REFS/PULL/"):
		return "git checkout -q --force " + shellQuote("refs/remotes/pull/"+ref[len("refs/pull/"):]) + "\n"
	case strings.HasPrefix(upper, "REFS/"):
		return "git checkout -q --force " + shellQuote(ref) + "\n"
	}
	// Unqualified, a branch wins over a tag of the same name
	return "if git show-ref --verify -q " + shellQuote("refs/remotes/origin/"+ref) + "; then git checkout -q --force -B " + shellQuote(ref) + " " + shellQuote("refs/remotes/origin/"+ref) +
		"; elif git show-ref --verify -q " + shellQuote("refs/tags/"+ref) + "; then git checkout -q --force " + shellQuote("refs/tags/"+ref) +
		"; else echo " + shellQuote("::error::A branch or tag with the name '"+ref+"' could not be found") + "; exit 1; fi\n"
}

// Ref git lfs fetches for, the start point of the checkout
func lfsTarget(ref, commit string) string {
	upper := strings.ToUpper(ref)
	switch {
	case commit != "":
		return commit
	case strings.HasPrefix(upper, "REFS/HEADS/"):
		return "refs/remotes/origin/" + ref[len("refs/heads/"):]
	case strings.HasPrefix(upper, "REFS/PULL/"):
		return "refs/remotes/pull/" + ref[len("refs/pull/"):]
	case ref == "":
		return "FETCH_HEAD"
	}
	return ref
}

func (j *job) checkout(ctx context.Context, name string, with, env, outputs map[string]string, out io.Writer) error {
	in, err := j.checkoutInput(with)
	if err != nil {
		return err
	}
	dest := j.workspace
	if in.Path != "" {
		dest = filepath.Join(j.workspace, filepath.FromSlash(in.Path))
		if !within(j.workspace, dest) {
			return fmt.Errorf("path %q leaves the workspace", in.Path)
		}
	}
	url := j.task.CloneUrl
	if in.Repository != j.task.RepoFullName {
		base := in.ServerURL
		if base == "" {
			base = j.cloneBase()
		}
		url = base + "/" + in.Repository + ".git"
	}
	// Queued before the clone so a failed checkout still cleans up
	j.registerPost(name, func(ctx context.Context, cw *commandWriter) error {
		return j.checkoutCleanup(ctx, dest, url, cw)
	})
	for _, unsupported := range []string{"ssh-key", "ssh-known-hosts"} {
		if strings.TrimSpace(with[unsupported]) != "" {
			fmt.Fprintf(out, "##[warning]checkout input %s is not supported by this runner and was ignored\n", unsupported)
		}
	}
	if in.Clean {
		if entries, _ := j.readDir(dest); len(entries) > 0 {
			fmt.Fprintf(out, "Deleting the contents of '%s'\n", j.pathOf(dest))
			if err := j.removeAll(dest); err != nil {
				return err
			}
		}
	}
	if err := j.mkdirAll(dest); err != nil {
		return err
	}
	fmt.Fprintf(out, "Syncing repository: %s\n", in.Repository)
	if j.exec.Has(ctx, "git") {
		return j.checkoutWithGit(ctx, in, url, dest, env, out)
	}
	fmt.Fprintln(out, "No git in the job environment, cloning from the runner host")
	// The host side clone needs a real directory
	if info, err := j.lstat(dest); err != nil || !info.IsDir() || !j.realInside(dest) {
		return fmt.Errorf("checkout path %q is not a directory inside the job", j.pathOf(dest))
	}
	target := in.Commit
	if target == "" {
		target = in.Ref
	}
	_, hash, err := cloneRef(ctx, j.r.http, j.hostURL(url), dest, target, in.FetchDepth, in.Token)
	if err != nil {
		return err
	}
	outputs["ref"] = in.Ref
	outputs["commit"] = hash
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Fetches inside the job environment the way actions/checkout does
func (j *job) checkoutWithGit(ctx context.Context, in checkoutInput, url, dest string, env map[string]string, out io.Writer) error {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	fmt.Fprintf(&sb, "cd %s\n", shellQuote(j.pathOf(dest)))
	if in.SetSafeDirectory {
		fmt.Fprintf(&sb, "git config --global --add safe.directory %s\n", shellQuote(j.pathOf(dest)))
	}
	sb.WriteString("git init -q .\n")
	sb.WriteString("git config --local gc.auto 0\n")
	fmt.Fprintf(&sb, "git remote add origin %s 2>/dev/null || git remote set-url origin %s\n", shellQuote(url), shellQuote(url))
	authKey, authValue := "", ""
	if in.Token != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(jobTokenUser + ":" + in.Token))
		j.log.AddMask(b64)
		authKey, authValue = gitAuthKey(url), "AUTHORIZATION: basic "+b64
		fmt.Fprintf(&sb, "git config --local %s %s\n", shellQuote(authKey), shellQuote(authValue))
	}
	if len(in.SparseCheckout) > 0 {
		if in.SparseCone {
			sb.WriteString("git sparse-checkout init --cone\n")
			sb.WriteString("git sparse-checkout set")
			for _, p := range in.SparseCheckout {
				sb.WriteString(" ")
				sb.WriteString(shellQuote(p))
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("git config core.sparseCheckout true\n")
			sb.WriteString("printf '%s\\n'")
			for _, p := range in.SparseCheckout {
				sb.WriteString(" ")
				sb.WriteString(shellQuote(p))
			}
			sb.WriteString(" > .git/info/sparse-checkout\n")
		}
	}
	if in.LFS {
		sb.WriteString("git lfs install --local\n")
	}
	fetch := "git fetch --prune --no-recurse-submodules"
	if !in.FetchTags {
		fetch += " --no-tags"
	}
	if in.Filter != "" {
		fetch += " --filter=" + shellQuote(in.Filter)
	}
	specs := func(list []string) string {
		var b strings.Builder
		for _, s := range list {
			b.WriteString(" ")
			b.WriteString(shellQuote(s))
		}
		return b.String()
	}
	if in.FetchDepth <= 0 {
		fmt.Fprintf(&sb, "%s origin%s\n", fetch, specs(allHistoryRefSpecs(in.Ref, in.Commit)))
		// A force push may have moved the ref past what the full fetch saw
		if in.Commit != "" {
			fmt.Fprintf(&sb, "%s origin%s\n", fetch, specs(refSpecs(in.Ref, in.Commit)))
		}
	} else {
		fmt.Fprintf(&sb, "%s --depth=%d origin%s\n", fetch, in.FetchDepth, specs(refSpecs(in.Ref, in.Commit)))
	}
	if in.LFS {
		fmt.Fprintf(&sb, "git lfs fetch origin %s\n", shellQuote(lfsTarget(in.Ref, in.Commit)))
	}
	sb.WriteString(checkoutLines(in.Ref, in.Commit))
	if in.Submodules != "" {
		recursive := ""
		if in.Submodules == "recursive" {
			recursive = " --recursive"
		}
		auth := ""
		if authKey != "" {
			auth = "-c " + shellQuote(authKey+"="+authValue) + " "
		}
		fmt.Fprintf(&sb, "git %ssubmodule sync%s\n", auth, recursive)
		depth := ""
		if in.FetchDepth > 0 {
			depth = fmt.Sprintf(" --depth=%d", in.FetchDepth)
		}
		fmt.Fprintf(&sb, "git %s-c protocol.version=2 submodule update --init --force%s%s\n", auth, depth, recursive)
	}
	if authKey != "" && !in.PersistCredentials {
		fmt.Fprintf(&sb, "git config --local --unset-all %s || true\n", shellQuote(authKey))
	}
	sb.WriteString("git log -1 --format='%H %s'\n")
	fmt.Fprintf(&sb, "printf 'ref=%%s\\ncommit=%%s\\n' %s \"$(git rev-parse HEAD)\" >> \"$GITHUB_OUTPUT\"\n", shellQuote(in.Ref))
	code, err := j.runScript(ctx, sb.String(), "sh", j.pathOf(dest), env, out)
	return exitErr(code, err)
}

// Config key actions/checkout keeps the token header under
func gitAuthKey(url string) string {
	scope := url
	if u, err := neturl.Parse(url); err == nil && u.Host != "" {
		scope = u.Scheme + "://" + u.Host + "/"
	}
	return "http." + scope + ".extraheader"
}

// Post job cleanup, drops the token header like actions/checkout
func (j *job) checkoutCleanup(ctx context.Context, dest, url string, out io.Writer) error {
	fmt.Fprintln(out, "Post job cleanup.")
	if _, err := j.stat(filepath.Join(dest, ".git", "config")); err != nil || !j.exec.Has(ctx, "git") {
		return nil
	}
	unset := "git config --local --unset-all " + shellQuote(gitAuthKey(url)) + " || :"
	script := "cd " + shellQuote(j.pathOf(dest)) + "\n" + unset + "\n" +
		"git submodule foreach --recursive " + shellQuote(unset) + " || :\n"
	code, err := j.runScript(ctx, script, "sh", j.pathOf(dest), j.baseEnv(), out)
	return exitErr(code, err)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
