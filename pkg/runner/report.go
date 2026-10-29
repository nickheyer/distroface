package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitea.com/gitea/runner/act/model"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	setupStepName    = "Set up job"
	completeStepName = "Complete job"
)

// One entry of the job annotations json array
type annotation struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Title   string `json:"title,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
	Col     int    `json:"col,omitempty"`
	EndCol  int    `json:"end_col,omitempty"`
	Step    int32  `json:"step,omitempty"`
}

var commandRe = regexp.MustCompile(`^::([^ :]+)( [^:]*)?::(.*)$`)

// Turns act log entries into the transcript, step table, and annotations
type reporter struct {
	j *job

	mu sync.Mutex
	// Job steps by number, names derive from them
	steps   []*model.Step
	rows    []*v1.JobStep
	byKey   map[string]int
	current int
	// Raw groups open inside the current row
	rawDepth int
	// Act's own step header group was dropped, the row is the header
	headerSwallowed bool
	// Raw lines seen since the current row opened
	rowLines    int
	annotations []annotation
	outputs     map[string]string
	jobResult   string
	stopToken   string
	lastReport  string
	dirty       bool
	wake        chan struct{}
}

func newReporter(j *job) *reporter {
	return &reporter{j: j, byKey: map[string]int{}, current: -1, outputs: map[string]string{}, wake: make(chan struct{}, 1)}
}

func (r *reporter) Levels() []logrus.Level { return logrus.AllLevels }

func (r *reporter) setSteps(steps []*model.Step) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = steps
}

// Set up job row and group, open until the first step starts
func (r *reporter) startSetup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addRowLocked("setup", setupStepName)
	r.markDirty(true)
}

// Setup failed before any step ran
func (r *reporter) failSetup(err error) v1.ActionsConclusion {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorLocked(err.Error(), r.currentNumber())
	r.endGroupLocked(v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
	r.markDirty(true)
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
}

// Complete job row once act returned
func (r *reporter) finish(conclusion v1.ActionsConclusion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	open := conclusion
	if conclusion == v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT {
		open = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	}
	r.endGroupLocked(open)
	for _, row := range r.rows {
		if row.Status == v1.ActionsStatus_ACTIONS_STATUS_COMPLETED {
			continue
		}
		row.Status, row.Conclusion, row.FinishedAt = v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED, timestampNow()
		if conclusion == v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED {
			row.Conclusion = conclusion
		}
	}
	r.addRowLocked("complete", completeStepName)
	r.j.log.Line("Cleaning up the job environment")
	r.endGroupLocked(v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	r.markDirty(true)
}

func (r *reporter) result() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobResult
}

func (r *reporter) setOutputs(outputs map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range outputs {
		r.outputs[k] = v
	}
	r.markDirty(false)
}

// Logs an error that belongs to the job rather than a step
func (r *reporter) errorLine(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorLocked(msg, 0)
}

func (r *reporter) errorLocked(msg string, step int32) {
	msg = r.j.log.Masked(strings.TrimPrefix(msg, "##[error]"))
	r.j.log.Line("##[error]%s", msg)
	r.annotations = append(r.annotations, annotation{Level: "error", Message: msg, Step: step})
	r.markDirty(false)
}

func (r *reporter) currentNumber() int32 {
	if r.current < 0 {
		return 0
	}
	return r.rows[r.current].Number
}

// Row for a stage and step, created and opened on first sight
func (r *reporter) addRowLocked(key, name string) int {
	if i, ok := r.byKey[key]; ok {
		return i
	}
	name = singleLine(name)
	r.endGroupLocked(v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	row := &v1.JobStep{Number: int32(len(r.rows) + 1), Name: name, Status: v1.ActionsStatus_ACTIONS_STATUS_RUNNING, StartedAt: timestampNow()}
	r.rows = append(r.rows, row)
	r.byKey[key] = len(r.rows) - 1
	r.current = len(r.rows) - 1
	r.rowLines = 0
	r.j.log.Line("##[group]%s", name)
	return r.current
}

// Completes the current row and ends its group, stray raw groups close first
func (r *reporter) endGroupLocked(conclusion v1.ActionsConclusion) {
	if r.current < 0 {
		return
	}
	r.closeRowLocked(r.current, conclusion)
	for ; r.rawDepth > 0; r.rawDepth-- {
		r.j.log.Line("##[endgroup]")
	}
	r.headerSwallowed = false
	r.j.log.Line("##[endgroup]")
	r.current = -1
}

// Completes a row, the group stays open for errors act reports after the result
func (r *reporter) closeRowLocked(row int, conclusion v1.ActionsConclusion) {
	s := r.rows[row]
	if s.Status != v1.ActionsStatus_ACTIONS_STATUS_COMPLETED {
		s.Status, s.Conclusion, s.FinishedAt = v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, conclusion, timestampNow()
	}
	r.markDirty(true)
}

// Act step entries carry a stage, number, and name
func rowKey(stage string, number int) string {
	return stage + ":" + strconv.Itoa(number)
}

// GitHub style names, Run plus uses or first script line
func (r *reporter) rowNameLocked(stage string, number int, stepName string) string {
	name := firstLine(stepName)
	if number >= 0 && number < len(r.steps) && r.steps[number] != nil {
		switch s := r.steps[number]; {
		case s.Name != "":
			name = singleLine(stepName)
		case s.Uses != "":
			name = strings.TrimSpace("Run " + singleLine(stepName))
		case s.Run != "":
			name = strings.TrimSpace("Run " + firstLine(stepName))
		}
	}
	switch stage {
	case "Pre":
		return "Pre " + name
	case "Post":
		return "Post " + name
	}
	return name
}

// Main always rows, Pre and Post only when they run
func stageRuns(stage string, level logrus.Level, raw, hasResult bool) bool {
	if stage != "Pre" && stage != "Post" {
		return true
	}
	return raw || level <= logrus.ErrorLevel || (hasResult && level < logrus.DebugLevel)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// Collapses line breaks so group markers stay one line
func singleLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " "))
}

// Composite action steps nest under their job step
func isJobStepEntry(entry *logrus.Entry) bool {
	if v, ok := entry.Data["stepID"].([]string); ok && len(v) > 1 {
		return false
	}
	return true
}

func resultString(v any) string {
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprint(v)
}

func conclusionOf(result string) v1.ActionsConclusion {
	switch result {
	case "success":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
	case "skipped":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED
	case "cancelled":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	}
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
}

// Fire receives every act log entry of the job
func (r *reporter) Fire(entry *logrus.Entry) error {
	msg := strings.TrimRight(entry.Message, "\r\n")
	raw, _ := entry.Data["raw_output"].(bool)
	stage, _ := entry.Data["stage"].(string)
	number, hasNumber := entry.Data["stepNumber"].(int)
	stepName, _ := entry.Data["step"].(string)
	result, hasResult := entry.Data["stepResult"]

	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := entry.Data["jobResult"]; ok {
		r.jobResult = resultString(v)
	}
	step := r.currentNumber()
	if hasNumber && isJobStepEntry(entry) {
		key := rowKey(stage, number)
		row, open := r.byKey[key]
		if !open && stageRuns(stage, entry.Level, raw, hasResult) {
			row, open = r.addRowLocked(key, r.rowNameLocked(stage, number, stepName)), true
		}
		if open {
			// Errors act reports after the result still belong to the step
			step = r.rows[row].Number
			if hasResult {
				r.closeRowLocked(row, conclusionOf(resultString(result)))
			}
		}
	}
	switch {
	case raw:
		r.rawLocked(msg)
	case entry.Level <= logrus.ErrorLevel:
		if msg != "" {
			r.errorLocked(msg, step)
		}
	case entry.Level == logrus.WarnLevel:
		r.j.log.Line("##[warning]%s", strings.TrimPrefix(msg, "##[warning]"))
	case entry.Level == logrus.InfoLevel:
		// Act narrates container setup, step output speaks for itself
		if _, result := entry.Data["jobResult"]; !hasNumber && !result {
			r.j.log.Line("%s", msg)
		}
	}
	return nil
}

// Step output, workflow commands become transcript markers and annotations
func (r *reporter) rawLocked(line string) {
	r.rowLines++
	m := commandRe.FindStringSubmatch(line)
	if m == nil {
		r.j.log.Line("%s", line)
		return
	}
	cmd, props, value := m[1], parseProps(strings.TrimSpace(m[2])), unescapeData(m[3])
	if r.stopToken != "" {
		if cmd == r.stopToken {
			r.stopToken = ""
			return
		}
		r.j.log.Line("%s", line)
		return
	}
	switch cmd {
	case "group":
		title := singleLine(value)
		// Act wraps the whole step in a Run header group, the row already is one
		if r.rowLines == 1 && r.current >= 0 && strings.HasPrefix(title, "Run ") {
			r.headerSwallowed = true
			return
		}
		r.rawDepth++
		r.j.log.Line("##[group]%s", title)
	case "endgroup":
		switch {
		case r.rawDepth > 0:
			r.rawDepth--
			r.j.log.Line("##[endgroup]")
		case r.headerSwallowed:
			r.headerSwallowed = false
		}
	case "error", "warning", "notice":
		a := annotation{Level: cmd, Message: r.j.log.Masked(value), Title: r.j.log.Masked(props["title"]), File: props["file"], Step: r.currentNumber()}
		a.Line, _ = strconv.Atoi(props["line"])
		a.EndLine, _ = strconv.Atoi(props["endLine"])
		a.Col, _ = strconv.Atoi(props["col"])
		a.EndCol, _ = strconv.Atoi(props["endColumn"])
		r.annotations = append(r.annotations, a)
		r.j.log.Line("##[%s]%s", cmd, value)
		r.markDirty(false)
	case "add-mask":
		r.j.log.AddMask(value)
	case "stop-commands":
		r.stopToken = value
	default:
		r.j.log.Line("%s", line)
	}
}

// key=value pairs after the command name
func parseProps(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			out[strings.TrimSpace(k)] = unescapeProp(strings.TrimSpace(v))
		}
	}
	return out
}

func unescapeData(s string) string {
	return strings.NewReplacer("%0D", "\r", "%0A", "\n", "%25", "%").Replace(s)
}

func unescapeProp(s string) string {
	return strings.NewReplacer("%0D", "\r", "%0A", "\n", "%3A", ":", "%2C", ",", "%25", "%").Replace(s)
}

func (r *reporter) markDirty(urgent bool) {
	r.dirty = true
	if urgent {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// Step table, outputs, and annotations as the server stores them
func (r *reporter) snapshot() ([]*v1.JobStep, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	steps := make([]*v1.JobStep, 0, len(r.rows))
	for _, s := range r.rows {
		steps = append(steps, proto.Clone(s).(*v1.JobStep))
	}
	outputs, _ := json.Marshal(r.outputs)
	ann, _ := json.Marshal(r.annotations)
	return steps, string(outputs), string(ann)
}

// Single state loop, each push also learns of cancels
func (r *reporter) daemon(ctx context.Context, stop <-chan struct{}) {
	t := time.NewTicker(stateReportEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-t.C:
		}
		r.sync(ctx)
	}
}

// Sends changed state, acts on a cancel the server answers with
func (r *reporter) sync(ctx context.Context) {
	r.mu.Lock()
	dirty := r.dirty
	r.dirty = false
	r.mu.Unlock()
	steps, _, ann := r.snapshot()
	key := reportKey(steps, ann)
	r.mu.Lock()
	unchanged := key == r.lastReport
	r.mu.Unlock()
	if unchanged && !dirty {
		steps, ann = nil, ""
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	cancelled, err := r.j.r.client.UpdateTask(rctx, r.j.task.JobId, steps, "", ann)
	if err != nil {
		r.j.r.out.Printf("job %s: update: %v", shortID(r.j.task.JobId), err)
		r.mu.Lock()
		r.dirty = true
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.lastReport = key
	r.mu.Unlock()
	if cancelled {
		r.j.requestCancel(serverCancelMsg)
	}
}

func timestampNow() *timestamppb.Timestamp { return timestamppb.New(time.Now()) }

// Stable encoding of a snapshot, equal keys mean nothing to resend
func reportKey(steps []*v1.JobStep, ann string) string {
	var sb strings.Builder
	opts := proto.MarshalOptions{Deterministic: true}
	for _, s := range steps {
		b, _ := opts.Marshal(s)
		sb.WriteByte(byte(len(b) >> 8))
		sb.WriteByte(byte(len(b)))
		sb.Write(b)
	}
	sb.WriteByte(0)
	sb.WriteString(ann)
	return sb.String()
}
