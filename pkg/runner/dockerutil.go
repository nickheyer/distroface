package runner

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	actcontainer "gitea.com/gitea/runner/act/container"
	"github.com/docker/cli/cli/connhelper"
	"github.com/moby/moby/client"
)

// Explicit daemon address, the environment when none is configured
func dockerClient(ctx context.Context, host string) (client.APIClient, error) {
	if host == "" {
		return actcontainer.GetDockerClient(ctx)
	}
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if strings.HasPrefix(host, "ssh://") {
		helper, err := connhelper.GetConnectionHelper(host)
		if err != nil {
			return nil, err
		}
		opts = append(opts, client.WithHost(helper.Host), client.WithDialContext(helper.Dialer))
	} else {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to docker daemon %s: %w", host, err)
	}
	return cli, nil
}

func pingDocker(ctx context.Context, cli client.APIClient) error {
	_, err := cli.Ping(ctx, client.PingOptions{})
	return err
}

var nonAlnum = regexp.MustCompile("[^a-zA-Z0-9]")

// Act's name rewrite, repeated until act leaves it unchanged
func containerNamePrefix(parts ...string) string {
	name := nonAlnum.ReplaceAllString(strings.Join(parts, "-"), "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-")
}

// Job container act started for a task
func findJobContainer(ctx context.Context, cli client.APIClient, prefix string) (string, error) {
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{Filters: make(client.Filters).Add("name", prefix)})
	if err != nil {
		return "", err
	}
	for _, c := range list.Items {
		if !containerNamed(c.Names, prefix) {
			continue
		}
		info, err := cli.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
		if err != nil {
			continue
		}
		if info.Container.Config == nil {
			continue
		}
		for _, e := range info.Container.Config.Env {
			if strings.HasPrefix(e, "RUNNER_TOOL_CACHE=") {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no job container named %s* is running", prefix)
}

func containerNamed(names []string, prefix string) bool {
	for _, n := range names {
		if strings.HasPrefix(strings.TrimPrefix(n, "/"), prefix) {
			return true
		}
	}
	return false
}

// Removes every container, volume, and network with the prefix, answering their names
func removeDockerLeftovers(ctx context.Context, cli client.APIClient, prefix string) []string {
	var removed []string
	if list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("name", prefix)}); err == nil {
		for _, c := range list.Items {
			if !containerNamed(c.Names, prefix) {
				continue
			}
			if _, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err == nil {
				removed = append(removed, "container "+strings.TrimPrefix(c.Names[0], "/"))
			}
		}
	}
	if list, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("name", prefix)}); err == nil {
		for _, n := range list.Items {
			if !strings.HasPrefix(n.Name, prefix) {
				continue
			}
			if _, err := cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}); err == nil {
				removed = append(removed, "network "+n.Name)
			}
		}
	}
	if list, err := cli.VolumeList(ctx, client.VolumeListOptions{Filters: make(client.Filters).Add("name", prefix)}); err == nil {
		for _, v := range list.Items {
			if !strings.HasPrefix(v.Name, prefix) {
				continue
			}
			if _, err := cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: true}); err == nil {
				removed = append(removed, "volume "+v.Name)
			}
		}
	}
	return removed
}

// Pulls one image, registry status and errors go to out
func pullImage(ctx context.Context, cli client.APIClient, image, auth string, out io.Writer) error {
	resp, err := cli.ImagePull(ctx, image, client.ImagePullOptions{RegistryAuth: auth})
	if err != nil {
		return err
	}
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
		if msg.Status == "" || msg.Progress != nil {
			continue
		}
		line := msg.Status
		if msg.ID != "" {
			line = msg.ID + ": " + msg.Status
		}
		if _, err := io.WriteString(out, line+"\n"); err != nil {
			return err
		}
	}
	return nil
}
