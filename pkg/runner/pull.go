package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gitea.com/gitea/runner/act/common"
	actcontainer "gitea.com/gitea/runner/act/container"
	"gitea.com/gitea/runner/act/model"
	actrunner "gitea.com/gitea/runner/act/runner"
	"github.com/distribution/reference"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"
	"github.com/sirupsen/logrus"
)

// Image act will start plus the workflow's credentials
type imagePull struct {
	image    string
	username string
	password string
}

// Pulls every job and service image before act starts
func (j *job) pullImages(ctx context.Context, cfg *actrunner.Config, workflow *model.Workflow, jobID string) error {
	lctx := common.WithLogger(ctx, j.setupLogger())
	pulls, err := j.jobImages(lctx, cfg, workflow, jobID)
	if err != nil {
		return err
	}
	if len(pulls) == 0 {
		return nil
	}
	cli, err := dockerClient(ctx, j.r.cfg.DockerHost)
	if err != nil {
		return err
	}
	defer cli.Close()
	done := map[string]bool{}
	for _, p := range pulls {
		key := p.username + "\x00" + p.image
		if done[key] {
			continue
		}
		done[key] = true
		auth, err := j.registryAuth(lctx, p)
		if err != nil {
			return fmt.Errorf("pull image %s: %w", p.image, err)
		}
		j.log.Line("Pulling image %s", p.image)
		if err := pullImage(ctx, cli, p.image, auth, j.log); err != nil {
			return fmt.Errorf("pull image %s: %w", p.image, err)
		}
	}
	return nil
}

// Resolved like act, container image beats the label image
func (j *job) jobImages(ctx context.Context, cfg *actrunner.Config, workflow *model.Workflow, jobID string) ([]imagePull, error) {
	run := &model.Run{Workflow: workflow, JobID: jobID}
	job := run.Job()
	if job.Strategy != nil {
		rc := &actrunner.RunContext{Config: cfg, Run: run, EventJSON: cfg.EventJSON, StepResults: map[string]*model.StepResult{}}
		if err := rc.NewExpressionEvaluator(ctx).EvaluateYamlNode(ctx, &job.Strategy.RawMatrix); err != nil {
			j.log.Line("##[warning]Matrix expressions kept literal: %v", err)
		}
	}
	matrixes, err := job.GetMatrixes()
	if err != nil {
		return nil, err
	}
	if len(matrixes) == 0 {
		matrixes = []map[string]any{nil}
	}
	serviceIDs := make([]string, 0, len(job.Services))
	for id := range job.Services {
		serviceIDs = append(serviceIDs, id)
	}
	sort.Strings(serviceIDs)
	var pulls []imagePull
	for _, matrix := range matrixes {
		rc := &actrunner.RunContext{Config: cfg, Run: run, EventJSON: cfg.EventJSON, StepResults: map[string]*model.StepResult{}, Matrix: matrix}
		ee := rc.NewExpressionEvaluator(ctx)
		image, username, password := "", cfg.Secrets["DOCKER_USERNAME"], cfg.Secrets["DOCKER_PASSWORD"]
		if c := job.Container(); c != nil {
			image = ee.Interpolate(ctx, c.Image)
			if c.Credentials != nil {
				if username, password, err = interpolateCredentials(ctx, ee, c.Credentials); err != nil {
					return nil, fmt.Errorf("container credentials: %w", err)
				}
			}
		}
		if image == "" {
			runsOn := job.RunsOn()
			for i, v := range runsOn {
				runsOn[i] = ee.Interpolate(ctx, v)
			}
			image = j.pickPlatform(runsOn)
		}
		if strings.EqualFold(image, "-self-hosted") {
			continue
		}
		pulls = append(pulls, imagePull{image: image, username: username, password: password})
		for _, id := range serviceIDs {
			spec := job.Services[id]
			var user, pass string
			if spec.Credentials != nil {
				if user, pass, err = interpolateCredentials(ctx, ee, spec.Credentials); err != nil {
					return nil, fmt.Errorf("service %s credentials: %w", id, err)
				}
			}
			pulls = append(pulls, imagePull{image: ee.Interpolate(ctx, spec.Image), username: user, password: pass})
		}
	}
	return pulls, nil
}

// Same checks act applies to a credentials block
func interpolateCredentials(ctx context.Context, ee actrunner.ExpressionEvaluator, creds map[string]string) (string, string, error) {
	if len(creds) != 2 {
		return "", "", errors.New("invalid property count for key 'credentials:'")
	}
	username := ee.Interpolate(ctx, creds["username"])
	if username == "" {
		return "", "", errors.New("failed to interpolate credentials.username")
	}
	password := ee.Interpolate(ctx, creds["password"])
	if password == "" {
		return "", "", errors.New("failed to interpolate credentials.password")
	}
	return username, password, nil
}

// Workflow credentials, then job token, then runner docker login
func (j *job) registryAuth(ctx context.Context, p imagePull) (string, error) {
	if p.username != "" && p.password != "" {
		return authconfig.Encode(registry.AuthConfig{Username: p.username, Password: p.password})
	}
	if host := registryHost(j.task.ServerUrl); host != "" && j.task.Token != "" && imageDomain(p.image) == host {
		return authconfig.Encode(registry.AuthConfig{Username: jobTokenUser, Password: j.task.Token})
	}
	ac, err := actcontainer.LoadDockerAuthConfig(ctx, p.image)
	if err != nil {
		return "", err
	}
	if ac.Username == "" && ac.Password == "" && ac.IdentityToken == "" && ac.RegistryToken == "" {
		return "", nil
	}
	return authconfig.Encode(ac)
}

// Registry of an image reference, docker.io for bare names
func imageDomain(image string) string {
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return ""
	}
	return reference.Domain(ref)
}

// Routes act helper logs to the reporter before act runs
func (j *job) setupLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.InfoLevel)
	l.AddHook(j.rep)
	return l
}
