package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage container images",
	}
	cmd.AddCommand(
		newImageListCmd(),
		newImageTagsCmd(),
	)
	return cmd
}

func printProtoJSON(messages []proto.Message) error {
	marshaler := protojson.MarshalOptions{EmitUnpopulated: true}
	out := make([]json.RawMessage, len(messages))
	for i, m := range messages {
		raw, err := marshaler.Marshal(m)
		if err != nil {
			return err
		}
		out[i] = raw
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func newImageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List image repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Repositories().ListRepositories(cmd.Context(), connect.NewRequest(&v1.ListRepositoriesRequest{
				Page: &v1.PageRequest{PageSize: 100},
			}))
			if err != nil {
				return rpcErr(err)
			}
			msgs := make([]proto.Message, len(resp.Msg.Repositories))
			for i, r := range resp.Msg.Repositories {
				msgs[i] = r
			}
			return printProtoJSON(msgs)
		},
	}
}

func newImageTagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tags [namespace/image]",
		Short: "List tags for an image (name must include its namespace)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, name, ok := strings.Cut(args[0], "/")
			if !ok {
				return fmt.Errorf("image must be qualified as namespace/name (e.g. myorg/app)")
			}
			resp, err := client.Repositories().ListTags(cmd.Context(), connect.NewRequest(&v1.ListTagsRequest{
				Namespace: namespace,
				Name:      name,
				Page:      &v1.PageRequest{PageSize: 100},
			}))
			if err != nil {
				return rpcErr(err)
			}
			msgs := make([]proto.Message, len(resp.Msg.Tags))
			for i, t := range resp.Msg.Tags {
				msgs[i] = t
			}
			return printProtoJSON(msgs)
		},
	}
}
