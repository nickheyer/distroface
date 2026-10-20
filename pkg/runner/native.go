package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Actions the runner implements itself instead of fetching
func (j *job) runNative(ctx context.Context, name string, with map[string]string, env map[string]string, outputs map[string]string, out io.Writer) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "actions/checkout":
		return true, j.checkout(ctx, with, env, out)
	case "actions/upload-artifact":
		return true, j.uploadArtifact(ctx, with, out)
	case "actions/download-artifact":
		return true, j.downloadArtifact(ctx, with, out)
	case "docker/login-action", "distroface/login":
		return true, j.dockerLogin(with, out)
	case "docker/setup-buildx-action":
		fmt.Fprintln(out, "BuildKit is provided by the runner")
		outputs["name"] = "default"
		outputs["driver"] = "docker-container"
		outputs["platforms"] = strings.Join(runnerPlatforms(), ",")
		return true, nil
	case "docker/setup-qemu-action":
		fmt.Fprintln(out, "Emulation depends on binfmt on the runner host")
		outputs["platforms"] = strings.Join(runnerPlatforms(), ",")
		return true, nil
	case "docker/build-push-action", "distroface/build-push":
		return true, j.buildPush(ctx, with, outputs, out)
	}
	return false, nil
}

// Stores registry credentials for later builds and docker calls
func (j *job) dockerLogin(with map[string]string, out io.Writer) error {
	key := dockerAuthKey(with["registry"])
	user, pass := strings.TrimSpace(with["username"]), with["password"]
	if user == "" && pass == "" {
		if key == registryHost(j.task.ServerUrl) && j.task.Token != "" {
			user, pass = jobTokenUser, j.task.Token
		} else {
			return fmt.Errorf("login to %s needs username and password", key)
		}
	}
	if pass == "" {
		return errors.New("password is required")
	}
	if user == "" {
		user = jobTokenUser
	}
	j.log.AddMask(pass)
	if err := j.setDockerAuth(key, user, pass); err != nil {
		return err
	}
	fmt.Fprintf(out, "Logged in to %s\n", key)
	return nil
}
