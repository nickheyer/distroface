package runner

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

// One entry of the job annotations json array
type Annotation struct {
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

// Interprets workflow command lines on the way to the log
type commandWriter struct {
	out          io.Writer
	onAnnotation func(Annotation)
	onMask       func(string)
	onOutput     func(key, value string)
	onState      func(key, value string)
	// Job matchers and the reader for add-matcher files
	matchers *matcherSet
	readFile func(string) ([]byte, error)
	// Workspace prefix stripped from matched file paths
	fileBase  string
	debug     bool
	echo      bool
	disabled  string
	stopToken string
	partial   []byte
	depth     int
}

func (w *commandWriter) Write(p []byte) (int, error) {
	data := append(w.partial, p...)
	w.partial = nil
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		w.line(string(data[:i]))
		data = data[i+1:]
	}
	if len(data) > 0 {
		w.partial = append([]byte(nil), data...)
	}
	return len(p), nil
}

// Emits a trailing partial line and closes open groups
func (w *commandWriter) Flush() {
	if len(w.partial) > 0 {
		w.line(string(w.partial))
		w.partial = nil
	}
	for w.depth > 0 {
		w.depth--
		io.WriteString(w.out, "##[endgroup]\n")
	}
}

func (w *commandWriter) line(line string) {
	text := strings.TrimRight(line, "\r")
	cmd, props, value, ok := parseCommand(text)
	if !ok {
		w.plain(line, text)
		return
	}
	if w.stopToken != "" {
		if cmd == w.stopToken {
			w.stopToken = ""
			return
		}
		w.plain(line, text)
		return
	}
	// Masks and the echo switch itself never echo
	switch cmd {
	case "echo":
		w.echo = strings.EqualFold(strings.TrimSpace(value), "on")
		return
	case "add-mask":
		if w.onMask != nil {
			w.onMask(value)
		}
		return
	}
	if w.echo {
		io.WriteString(w.out, line+"\n")
	}
	switch cmd {
	case "group":
		w.depth++
		io.WriteString(w.out, "##[group]"+value+"\n")
	case "endgroup":
		if w.depth > 0 {
			w.depth--
		}
		io.WriteString(w.out, "##[endgroup]\n")
	case "error", "warning", "notice":
		a := Annotation{Level: cmd, Message: value, Title: props["title"], File: props["file"]}
		a.Line = atoi(props["line"])
		a.EndLine = atoi(props["endLine"])
		a.Col = atoi(props["col"])
		a.EndCol = atoi(props["endColumn"])
		if w.onAnnotation != nil {
			w.onAnnotation(a)
		}
		io.WriteString(w.out, "##["+cmd+"]"+value+"\n")
	case "debug":
		if w.debug {
			io.WriteString(w.out, "##[debug]"+value+"\n")
		}
	case "stop-commands":
		w.stopToken = value
	case "set-output":
		if w.onOutput != nil {
			w.onOutput(props["name"], value)
		}
	case "save-state":
		if w.onState != nil {
			w.onState(props["name"], value)
		}
	case "add-matcher":
		w.addMatcher(value)
	case "remove-matcher":
		owner := strings.TrimSpace(props["owner"])
		if owner != "" && w.matchers != nil {
			w.matchers.remove(owner)
			io.WriteString(w.out, "Removed matchers: "+quoteOwners([]string{owner})+"\n")
		}
	case "set-env", "add-path":
		// Log injection reaches these, the files cannot be forged from output
		w.disabled = cmd
		msg := "The `" + cmd + "` command is disabled, write to $GITHUB_ENV or $GITHUB_PATH instead"
		if w.onAnnotation != nil {
			w.onAnnotation(Annotation{Level: "error", Message: msg})
		}
		io.WriteString(w.out, "##[error]"+msg+"\n")
	default:
		if !w.echo {
			io.WriteString(w.out, line+"\n")
		}
	}
}

// Ordinary output, flagged when a problem matcher recognises it
func (w *commandWriter) plain(line, text string) {
	if a := w.matchers.match(text, w.fileBase); a != nil {
		if w.onAnnotation != nil {
			w.onAnnotation(*a)
		}
		io.WriteString(w.out, "##["+a.Level+"]"+line+"\n")
		return
	}
	io.WriteString(w.out, line+"\n")
}

// Loads a matcher file and registers its owners
func (w *commandWriter) addMatcher(file string) {
	file = strings.TrimSpace(file)
	if w.matchers == nil || w.readFile == nil || file == "" {
		return
	}
	data, err := w.readFile(file)
	var list []*problemMatcher
	if err == nil {
		list, err = parseMatchers(data)
	}
	if err != nil {
		io.WriteString(w.out, "##[warning]Unable to process matcher file "+file+": "+err.Error()+"\n")
		return
	}
	w.matchers.add(list)
	owners := make([]string, 0, len(list))
	for _, m := range list {
		owners = append(owners, m.owner)
	}
	io.WriteString(w.out, "Added matchers: "+quoteOwners(owners)+". Problem matchers scan action output for known warning or error strings and report them inline.\n")
}

// Error once a disabled command was seen, nil otherwise
func (w *commandWriter) failure() error {
	if w.disabled == "" {
		return nil
	}
	return errors.New("the `" + w.disabled + "` workflow command is disabled")
}

// Splits a command, :: or legacy ##[ spelling
func parseCommand(line string) (cmd string, props map[string]string, value string, ok bool) {
	if strings.HasPrefix(line, "##[") {
		return parseLegacyCommand(line)
	}
	if !strings.HasPrefix(line, "::") {
		return "", nil, "", false
	}
	rest := line[2:]
	end := strings.Index(rest, "::")
	if end < 0 {
		return "", nil, "", false
	}
	head := rest[:end]
	value = unescapeData(rest[end+2:])
	props = map[string]string{}
	cmd, propText, _ := strings.Cut(head, " ")
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", nil, "", false
	}
	for _, kv := range strings.Split(propText, ",") {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		props[strings.TrimSpace(k)] = unescapeProperty(v)
	}
	return cmd, props, value, true
}

// ##[name key=value;key2=value2]data
func parseLegacyCommand(line string) (cmd string, props map[string]string, value string, ok bool) {
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return "", nil, "", false
	}
	head := line[3:end]
	value = unescapeLegacy(line[end+1:])
	props = map[string]string{}
	cmd, propText, _ := strings.Cut(head, " ")
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", nil, "", false
	}
	for _, kv := range strings.Split(propText, ";") {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		props[strings.TrimSpace(k)] = unescapeLegacy(v)
	}
	return cmd, props, value, true
}

func unescapeData(s string) string {
	s = strings.ReplaceAll(s, "%0D", "\r")
	s = strings.ReplaceAll(s, "%0A", "\n")
	return strings.ReplaceAll(s, "%25", "%")
}

func unescapeProperty(s string) string {
	s = strings.ReplaceAll(s, "%3A", ":")
	s = strings.ReplaceAll(s, "%2C", ",")
	return unescapeData(s)
}

func unescapeLegacy(s string) string {
	s = strings.ReplaceAll(s, "%3B", ";")
	s = strings.ReplaceAll(s, "%5D", "]")
	s = strings.ReplaceAll(s, "%0D", "\r")
	return strings.ReplaceAll(s, "%0A", "\n")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Reads GITHUB_OUTPUT style files including heredoc values
func parseKeyValueFile(data []byte) [][2]string {
	var out [][2]string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if key, delim, found := strings.Cut(line, "<<"); found && !strings.Contains(key, "=") {
			key = strings.TrimSpace(key)
			delim = strings.TrimSpace(delim)
			var lines []string
			for sc.Scan() {
				l := sc.Text()
				if strings.TrimSpace(l) == delim {
					break
				}
				lines = append(lines, l)
			}
			out = append(out, [2]string{key, strings.Join(lines, "\n")})
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		out = append(out, [2]string{strings.TrimSpace(key), value})
	}
	return out
}
