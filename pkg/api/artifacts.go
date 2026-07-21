package api

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newArtifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage artifacts and artifact repositories",
		Long: `Manage artifacts and artifact repositories.

Repositories are addressed as [namespace/]name. Bare names resolve on
the server, your own namespace first, then the unique visible match,
qualify the name when it is ambiguous.`,
	}
	cmd.AddCommand(
		newArtifactRepoCreateCmd(),
		newArtifactRepoListCmd(),
		newArtifactUploadCmd(),
		newArtifactDownloadCmd(),
		newArtifactDeleteCmd(),
		newArtifactSearchCmd(),
	)
	return cmd
}

// Applies the namespace flag to bare refs, the server resolves the rest
func repoArg(arg, namespace string) RepoRef {
	ref := parseRepoRef(arg)
	if ref.Namespace == "" {
		ref.Namespace = namespace
	}
	return ref
}

func newArtifactRepoCreateCmd() *cobra.Command {
	var description, namespace string
	var private bool

	cmd := &cobra.Command{
		Use:   "create [repo]",
		Short: "Create a new artifact repository",
		Long: `Create an artifact repository. Bare names land in your personal
namespace, use org/name or --namespace to target an organization.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := parseRepoRef(args[0])
			if ref.Namespace == "" {
				ref.Namespace = namespace
			}
			repo, err := client.createArtifactRepo(cmd.Context(), ref, description, private)
			if err != nil {
				return fmt.Errorf("failed to create repository: %w", err)
			}
			fmt.Printf("Created repository %s\n", repo.FullName)
			return nil
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Repository description")
	cmd.Flags().BoolVarP(&private, "private", "p", false, "Make repository private")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Target namespace (user or organization)")
	return cmd
}

func newArtifactRepoListCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List artifact repositories visible to you",
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := client.listArtifactRepos(cmd.Context(), namespace)
			if err != nil {
				return err
			}
			return printJSON(repos)
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Filter by namespace")
	return cmd
}

func newArtifactUploadCmd() *cobra.Command {
	var version, path, namespace string
	var properties map[string]string

	cmd := &cobra.Command{
		Use:   "upload [repo] [file]",
		Short: "Upload an artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := repoArg(args[0], namespace)
			file := args[1]

			if version == "" {
				version = filepath.Base(file)
			}
			version = sanitizeVersion(version)
			if path == "" {
				path = filepath.Base(file)
			}
			path = sanitizeFilePath(path)

			fmt.Printf("Uploading %s to %s (version: %s, path: %s)\n", file, ref, version, path)
			if err := client.uploadArtifact(cmd.Context(), ref, file, version, path, properties); err != nil {
				return fmt.Errorf("upload failed: %w", err)
			}
			fmt.Println("Upload successful")
			return nil
		},
	}

	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version")
	cmd.Flags().StringVarP(&path, "path", "p", "", "Artifact path in repository")
	cmd.Flags().StringToStringVar(&properties, "property", nil, "Properties (key=value,key=value,...)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace (user or organization)")
	return cmd
}

func newArtifactDownloadCmd() *cobra.Command {
	var (
		version   string
		artPath   string
		namespace string
		output    string
		props     map[string]string
		num       int
		sortBy    string
		order     string
		format    string
		unpack    bool
		flat      bool
	)

	cmd := &cobra.Command{
		Use:   "download [repo]",
		Short: "Download artifacts via query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := repoArg(args[0], namespace)

			q := make(url.Values)
			for key, value := range props {
				q.Set(key, value)
			}
			if version != "" {
				q.Set("version", version)
			}
			if artPath != "" {
				q.Set("path", artPath)
			}
			if num > 0 {
				q.Set("num", strconv.Itoa(num))
			}
			if sortBy != "" {
				q.Set("sort", sortBy)
			}
			if order != "" {
				q.Set("order", order)
			}
			if format != "" {
				q.Set("format", format)
			}
			if flat {
				q.Set("flat", "1")
			}

			if output == "" {
				output = "."
			}
			return client.downloadArtifacts(cmd.Context(), ref, q, output, unpack, flat, format)
		},
	}

	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version filter")
	cmd.Flags().StringVarP(&artPath, "path", "p", "", "Path inside artifact version")
	cmd.Flags().StringToStringVar(&props, "property", nil, "Properties (key=value)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output path (file or directory)")
	cmd.Flags().IntVar(&num, "num", 1, "Number of matching artifacts")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort field")
	cmd.Flags().StringVar(&order, "order", "", "Sort order (ASC/DESC)")
	cmd.Flags().StringVar(&format, "format", "zip", "Archive format (zip/tar.gz)")
	cmd.Flags().BoolVar(&unpack, "unpack", false, "Unpack downloaded archives")
	cmd.Flags().BoolVar(&flat, "flat", false, "Flatten directory structure")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace (user or organization)")
	return cmd
}

func newArtifactDeleteCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "delete [repo] [version] [path]",
		Short: "Delete an artifact",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := repoArg(args[0], namespace)
			if err := client.deleteArtifact(cmd.Context(), ref, args[1], args[2]); err != nil {
				return fmt.Errorf("failed to delete artifact: %w", err)
			}
			fmt.Println("Artifact deleted successfully")
			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace (user or organization)")
	return cmd
}

func newArtifactSearchCmd() *cobra.Command {
	var (
		repo      string
		name      string
		version   string
		artPath   string
		namespace string
		props     map[string]string
		num       int
		offset    int
		sortBy    string
		order     string
		table     bool
	)

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search for artifacts",
		Long: `Search artifacts across every repository visible to you, or within
one repository with --repo. Name, version, path, and property filters
narrow the results.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := SearchOptions{
				Name:       name,
				Version:    version,
				Path:       artPath,
				Properties: props,
				Num:        num,
				Offset:     offset,
				Sort:       sortBy,
				Order:      order,
			}
			if repo != "" {
				opts.Ref = repoArg(repo, namespace)
			} else {
				opts.Ref.Namespace = namespace
			}

			search, err := client.searchArtifacts(cmd.Context(), opts)
			if err != nil {
				// V1 behavior, search errors degrade to empty results
				debugf("search error: %v", err)
				search = &SearchResponse{Results: []Artifact{}}
			}

			if table {
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "Total Matches:", search.Total)
				fmt.Fprintln(w, "\nREPOSITORY\tNAME\tVERSION\tSIZE\tUPDATED")
				for _, a := range search.Results {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
						a.RepoID, a.Name, a.Version, formatSize(a.Size), a.UpdatedAt.Format(time.RFC3339))
				}
				return w.Flush()
			}
			return printJSON(search)
		},
	}

	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Repository, optionally qualified as namespace/name")
	cmd.Flags().StringVar(&name, "name", "", "Artifact name filter")
	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version filter")
	cmd.Flags().StringVarP(&artPath, "path", "p", "", "Path inside artifact version")
	cmd.Flags().StringToStringVar(&props, "property", nil, "Properties (key=value,key=value,...)")
	cmd.Flags().IntVar(&num, "num", 0, "Max number of matching artifacts (default all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Result offset for pagination")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort field (default created_at)")
	cmd.Flags().StringVar(&order, "order", "", "Sort order (ASC or DESC)")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace (user or organization)")
	return cmd
}
