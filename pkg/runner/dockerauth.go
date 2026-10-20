package runner

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Docker Hub key as docker config spells it
const dockerHubAuthKey = "https://index.docker.io/v1/"

// Username the instance registry accepts for job tokens
const jobTokenUser = "x-access-token"

// One registry login held for the job
type dockerAuth struct {
	Username string
	Password string
}

// Registry input normalised to a docker config auths key
func dockerAuthKey(registry string) string {
	r := strings.TrimSpace(registry)
	r = strings.TrimSuffix(r, "/")
	switch strings.ToLower(r) {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io", "https://index.docker.io/v1", "https://index.docker.io", "http://index.docker.io/v1":
		return dockerHubAuthKey
	}
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(strings.ToLower(r), scheme) {
			r = r[len(scheme):]
		}
	}
	return r
}

// Directory docker and buildkit read credentials from
func (j *job) dockerConfigDir() string {
	return filepath.Join(j.tempDir, "_docker")
}

// Records a login and rewrites config.json
func (j *job) setDockerAuth(key, username, password string) error {
	j.mu.Lock()
	j.dockerAuths[key] = dockerAuth{Username: username, Password: password}
	j.mu.Unlock()
	return j.writeDockerConfig()
}

func (j *job) writeDockerConfig() error {
	dir := j.dockerConfigDir()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o777)
	j.mu.Lock()
	auths := make(map[string]dockerAuth, len(j.dockerAuths))
	for k, v := range j.dockerAuths {
		auths[k] = v
	}
	j.mu.Unlock()
	data, err := dockerConfigJSON(auths)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, data, 0o666); err != nil {
		return err
	}
	_ = os.Chmod(p, 0o666)
	return nil
}

// Serialises auths the way docker login writes them
func dockerConfigJSON(auths map[string]dockerAuth) ([]byte, error) {
	keys := make([]string, 0, len(auths))
	for k := range auths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	entries := map[string]map[string]string{}
	for _, k := range keys {
		a := auths[k]
		entries[k] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))}
	}
	out["auths"] = entries
	return json.MarshalIndent(out, "", "  ")
}

// Seeds the instance registry login from the job token
func (j *job) seedDockerAuth() error {
	host := registryHost(j.task.ServerUrl)
	if j.task.Token != "" && host != "" {
		j.mu.Lock()
		j.dockerAuths[host] = dockerAuth{Username: jobTokenUser, Password: j.task.Token}
		j.mu.Unlock()
	}
	return j.writeDockerConfig()
}
