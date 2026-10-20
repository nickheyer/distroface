package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func printProto(m proto.Message) error {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	fmt.Println(buf.String())
	return nil
}

// Tabwriter rows when table is set, proto json otherwise
func printOrTable[T proto.Message](table bool, items []T, header []string, row func(T) []string) error {
	if !table {
		msgs := make([]proto.Message, len(items))
		for i, item := range items {
			msgs[i] = item
		}
		return printProtoJSON(msgs)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, item := range items {
		fmt.Fprintln(w, strings.Join(row(item), "\t"))
	}
	return w.Flush()
}

// Follows next page tokens until the server runs dry
func allPages[T any](fetch func(token string) ([]T, string, error)) ([]T, error) {
	var out []T
	token := ""
	for {
		items, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		token = next
	}
}

func fmtTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().Local().Format(time.RFC3339)
}

// Lower case enum value without its type prefix
func enumLabel(value fmt.Stringer, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(value.String(), prefix))
}

// Issue and pull numbers, a leading hash is tolerated
func parseNumber(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return n, nil
}

// Repo plus number pair shared by issue and pull commands
func repoNumberArgs(args []string) (RepoRef, int64, error) {
	ref, err := gitRepoArg(args[0], "")
	if err != nil {
		return ref, 0, err
	}
	n, err := parseNumber(args[1])
	return ref, n, err
}

// Raw proto json for mixing into plain json output
func protoJSONValue(m proto.Message) json.RawMessage {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func protoJSONValues[T proto.Message](items []T) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, item := range items {
		out[i] = protoJSONValue(item)
	}
	return out
}
