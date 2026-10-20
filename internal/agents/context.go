package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"slices"
	"strings"

	"github.com/nickheyer/distroface/internal/agents/llm"
)

const (
	pruneAfter           = 10    // Assistant turns a result survives before a stub replaces it
	pruneBatchChars      = 32000 // Eligible characters piled up before a batch is stubbed
	stubArgsLen          = 80    // Longest argument excerpt a stub carries
	maxResultBytes       = 30000 // Bytes of one tool result that reach the model
	resultHeadBytes      = 12000 // Head bytes of a cut result, the rest is tail
	dedupMinBytes        = 500   // Shortest result worth pointing at an earlier copy
	displayCharsPerToken = 4     // Characters per token when sizes are shown
)

// One executed call as the transcript holds it
type toolRecord struct {
	id, name string
	args     []byte
	text     string
	ok       bool
}

// Stubs old results in batches, every eligible one when all
func (rc *runContext) prune(msgs []llm.Message, all bool) []llm.Message {
	calls := map[string]llm.ToolCall{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = tc
		}
	}
	var pool []int
	pending, turns := 0, 0
	for i, m := range slices.Backward(msgs) {

		if m.Role == llm.RoleAssistant {
			turns++
			continue
		}
		if m.Role != llm.RoleTool || turns < pruneAfter {
			continue
		}
		if _, done := rc.pruned[m.ToolCallID]; done {
			continue
		}
		pool = append(pool, i)
		pending += len(m.Content)
	}
	if len(pool) > 0 && (all || pending >= pruneBatchChars) {
		if rc.pruned == nil {
			rc.pruned = map[string]string{}
		}
		for _, i := range pool {
			rc.pruned[msgs[i].ToolCallID] = pruneStub(calls[msgs[i].ToolCallID], msgs[i])
		}
	}
	if len(rc.pruned) == 0 {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role != llm.RoleTool {
			continue
		}
		if stub, done := rc.pruned[out[i].ToolCallID]; done {
			out[i].Content = stub
		}
	}
	return out
}

// One line stub naming the call and dropped size
func pruneStub(call llm.ToolCall, m llm.Message) string {
	name := call.Name
	if name == "" {
		name = m.ToolName
	}
	lines := 0
	if m.Content != "" {
		lines = strings.Count(m.Content, "\n") + 1
	}
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(name)
	if args := excerptArgs(call.Arguments); args != "" {
		b.WriteString(" ")
		b.WriteString(args)
	}
	fmt.Fprintf(&b, " result dropped from context, %d lines, about %s tokens, run the call again if it is needed]", lines, roughTokens(len(m.Content)))
	return b.String()
}

// Compact arguments cut to the stub excerpt length
func excerptArgs(args json.RawMessage) string {
	s := string(compactJSON(args))
	if s == "" || s == "{}" || s == "null" {
		return ""
	}
	if runes := []rune(s); len(runes) > stubArgsLen {
		s = string(runes[:stubArgsLen]) + "..."
	}
	return s
}

// Plain count under a thousand, rounded thousands above
func roughTokens(chars int) string {
	n := chars / displayCharsPerToken
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%dk", (n+500)/1000)
}

// Whitespace free json, raw bytes when not json
func compactJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

// Newest turn keeps reasoning only while its prefix is unchanged
func (rc *runContext) bindReasoning(req *llm.Request) {
	last := -1
	for i, m := range req.Messages {
		if m.Role == llm.RoleAssistant {
			last = i
		}
	}
	for i := range req.Messages {
		if i != last {
			req.Messages[i].Reasoning = nil
		}
	}
	if last >= 0 && req.Messages[last].Reasoning != nil && (rc.bound == "" || binding(req, last) != rc.bound) {
		req.Messages[last].Reasoning = nil
	}
	rc.sent = binding(req, len(req.Messages))
}

// Digest of the request prefix a signature is bound to
func binding(req *llm.Request, upto int) string {
	d := digest{sha256.New()}
	d.str(req.Model)
	d.str(req.System)
	d.num(len(req.Tools))
	for _, t := range req.Tools {
		d.str(t.Name)
		d.str(t.Description)
		d.bytes(t.Parameters)
	}
	d.num(upto)
	for _, m := range req.Messages[:upto] {
		d.str(m.Role)
		d.str(m.Content)
		d.str(m.ToolCallID)
		d.str(m.ToolName)
		d.num(len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			d.str(tc.ID)
			d.str(tc.Name)
			d.bytes(tc.Arguments)
		}
	}
	return string(d.Sum(nil))
}

// Length prefixes every field so digests never collide
type digest struct{ hash.Hash }

func (d digest) num(n int) { d.Write(binary.AppendVarint(nil, int64(n))) }

func (d digest) str(s string) {
	d.num(len(s))
	io.WriteString(d, s)
}

func (d digest) bytes(b []byte) {
	d.num(len(b))
	d.Write(b)
}

// Points a repeated read at its earlier identical result
func (rc *runContext) dedup(call llm.ToolCall, spec *toolSpec, text string) string {
	if spec == nil || !spec.Read || len(text) < dedupMinBytes {
		return text
	}
	args := compactJSON(call.Arguments)
	for _, r := range rc.results {
		if r.id == call.ID || !r.ok || r.name != call.Name || !bytes.Equal(r.args, args) || r.text != text {
			continue
		}
		if _, pruned := rc.pruned[r.id]; pruned {
			continue
		}
		return fmt.Sprintf("[unchanged since the earlier %s call %s with the same arguments, its result above still applies]", call.Name, r.id)
	}
	return text
}

// Cuts the middle of an oversized result on line boundaries
func capResult(text string) string {
	if len(text) <= maxResultBytes {
		return text
	}
	head := text[:resultHeadBytes]
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i+1]
	}
	tail := text[len(text)-(maxResultBytes-resultHeadBytes):]
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	omitted := len(text) - len(head) - len(tail)
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return fmt.Sprintf("%s[%d of %d bytes omitted from the middle, narrow the call to see them]\n%s", head, omitted, len(text), tail)
}
