package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Manage releases of a git repository",
	}
	cmd.AddCommand(
		newReleaseCreateCmd(),
		newReleaseListCmd(),
		newReleaseViewCmd(),
		newReleaseUploadCmd(),
		newReleaseDownloadCmd(),
		newReleaseAttachImageCmd(),
		newReleaseDeleteCmd(),
	)
	return cmd
}

func newReleaseCreateCmd() *cobra.Command {
	var title, body, target string
	var draft, prerelease bool

	cmd := &cobra.Command{
		Use:   "create namespace/name tag",
		Short: "Create a release, the tag is created at --target when missing",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Releases().CreateRelease(cmd.Context(), &v1.CreateReleaseRequest{
				Namespace:  ref.Namespace,
				Name:       ref.Name,
				Tag:        args[1],
				Target:     target,
				Title:      title,
				Body:       body,
				Draft:      draft,
				Prerelease: prerelease,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetRelease())
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Release title, defaults to the tag")
	cmd.Flags().StringVar(&body, "body", "", "Release notes")
	cmd.Flags().StringVar(&target, "target", "", "Branch or sha to tag when the tag does not exist")
	cmd.Flags().BoolVar(&draft, "draft", false, "Create as a draft")
	cmd.Flags().BoolVar(&prerelease, "prerelease", false, "Mark as a prerelease")
	return cmd
}

func newReleaseListCmd() *cobra.Command {
	var table, drafts, prereleases bool

	cmd := &cobra.Command{
		Use:   "list namespace/name",
		Short: "List releases",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			releases, err := allPages(func(token string) ([]*v1.Release, string, error) {
				resp, err := client.Releases().ListReleases(cmd.Context(), &v1.ListReleasesRequest{
					Page:               &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
					Namespace:          ref.Namespace,
					Name:               ref.Name,
					IncludeDrafts:      drafts,
					IncludePrereleases: prereleases,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetReleases(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			return printOrTable(table, releases, []string{"TAG", "NAME", "STATE", "ASSETS", "PUBLISHED"}, func(r *v1.Release) []string {
				state := "published"
				if r.GetDraft() {
					state = "draft"
				} else if r.GetPrerelease() {
					state = "prerelease"
				}
				return []string{r.GetTag(), r.GetName(), state, strconv.Itoa(len(r.GetAssets())), fmtTime(r.GetPublishedAt())}
			})
		},
	}

	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "Include drafts")
	cmd.Flags().BoolVar(&prereleases, "prereleases", true, "Include prereleases")
	return cmd
}

func newReleaseViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view namespace/name tag",
		Short: "Show a release",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Releases().GetRelease(cmd.Context(), &v1.GetReleaseRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Tag:       args[1],
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetRelease())
		},
	}
}

func newReleaseUploadCmd() *cobra.Command {
	var label string
	var replace bool
	cmd := &cobra.Command{
		Use:   "upload namespace/name tag file...",
		Short: "Stream files onto a release as assets",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			tag := args[1]
			for _, file := range args[2:] {
				name := filepath.Base(file)
				fmt.Printf("Uploading %s to %s@%s\n", file, ref, tag)
				asset, err := client.uploadReleaseAsset(cmd.Context(), ref, tag, file, name, label, replace)
				if err != nil {
					return fmt.Errorf("upload %s failed: %w", file, err)
				}
				fmt.Printf("Attached %s (%s)\n", asset.GetName(), formatSize(asset.GetSize()))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Display label for the uploaded assets")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace an asset that already carries the file name")
	return cmd
}

func newReleaseDownloadCmd() *cobra.Command {
	var output, pattern string

	cmd := &cobra.Command{
		Use:   "download namespace/name tag",
		Short: "Download the assets of a release",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Releases().GetRelease(cmd.Context(), &v1.GetReleaseRequest{Namespace: ref.Namespace, Name: ref.Name, Tag: args[1]})
			if err != nil {
				return rpcErr(err)
			}
			if output == "" {
				output = "."
			}
			if err := os.MkdirAll(output, 0o755); err != nil {
				return err
			}
			n := 0
			for _, a := range resp.GetRelease().GetAssets() {
				if pattern != "" {
					if ok, _ := filepath.Match(pattern, a.GetName()); !ok {
						continue
					}
				}
				dest := filepath.Join(output, filepath.Base(a.GetName()))
				fmt.Printf("Downloading %s (%s)\n", a.GetName(), formatSize(a.GetSize()))
				if err := client.downloadReleaseAsset(cmd.Context(), a.GetDownloadUrl(), dest); err != nil {
					return fmt.Errorf("download %s failed: %w", a.GetName(), err)
				}
				n++
			}
			if n == 0 {
				return fmt.Errorf("release %s has no assets%s", args[1], patternNote(pattern))
			}
			fmt.Printf("Downloaded %d asset(s) to %s\n", n, output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Directory to write the assets into (default current)")
	cmd.Flags().StringVar(&pattern, "asset", "", "Only assets whose name matches this glob")
	return cmd
}

func patternNote(pattern string) string {
	if pattern == "" {
		return ""
	}
	return " matching " + pattern
}

// Streams one file to the release asset endpoint
func (c *Client) uploadReleaseAsset(ctx context.Context, ref RepoRef, tag, file, name, label string, replace bool) (*v1.ReleaseAsset, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	q := url.Values{"name": {name}}
	if label != "" {
		q.Set("label", label)
	}
	if replace {
		q.Set("replace", "1")
	}
	endpoint := "/" + url.PathEscape(ref.Namespace) + "/" + url.PathEscape(ref.Name) + "/-/releases/" + url.PathEscape(tag) + "/assets?" + q.Encode()
	resp, err := c.doData(ctx, http.MethodPost, endpoint, f)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	asset := &v1.ReleaseAsset{}
	if err := protojson.Unmarshal(body, asset); err != nil {
		return nil, fmt.Errorf("decode asset: %w", err)
	}
	return asset, nil
}

// Fetches one asset by its download url into a file
func (c *Client) downloadReleaseAsset(ctx context.Context, downloadURL, dest string) error {
	path := strings.TrimPrefix(downloadURL, strings.TrimRight(c.BaseURL, "/"))
	if path == downloadURL {
		if u, err := url.Parse(downloadURL); err == nil {
			path = u.RequestURI()
		}
	}
	resp, err := c.doData(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dfcli-asset-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

func parseImageRef(s, namespace string) (name, tag string, err error) {
	name, tag, ok := strings.Cut(s, ":")
	if !ok || tag == "" {
		return "", "", fmt.Errorf("image must include a tag, e.g. %s/app:1.0", namespace)
	}
	if ns, rest, ok := strings.Cut(name, "/"); ok && ns == namespace {
		name = rest
	}
	if name == "" {
		return "", "", fmt.Errorf("image name is empty")
	}
	return name, tag, nil
}

func newReleaseAttachImageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach-image namespace/name tag image:tag",
		Short: "Attach a container image from the same namespace to a release",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			imageName, imageTag, err := parseImageRef(args[2], ref.Namespace)
			if err != nil {
				return err
			}
			resp, err := client.Releases().AttachReleaseImage(cmd.Context(), &v1.AttachReleaseImageRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Tag:       args[1],
				ImageName: imageName,
				ImageTag:  imageTag,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetImage())
		},
	}
}

func newReleaseDeleteCmd() *cobra.Command {
	var deleteTag bool

	cmd := &cobra.Command{
		Use:   "delete namespace/name tag",
		Short: "Delete a release",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			_, err = client.Releases().DeleteRelease(cmd.Context(), &v1.DeleteReleaseRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Tag:       args[1],
				DeleteTag: deleteTag,
			})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Deleted release %s of %s\n", args[1], ref)
			return nil
		},
	}

	cmd.Flags().BoolVar(&deleteTag, "delete-tag", false, "Also delete the git tag")
	return cmd
}
