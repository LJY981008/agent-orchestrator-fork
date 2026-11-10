package ladologwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits of one incident (plan step 3).
const (
	// detectBeforeLines is how many lines before an incident it keeps.
	detectBeforeLines = 10
	// detectAfterLines is how many lines after an incident it keeps.
	detectAfterLines = 5
	// detectMaxLines caps the lines of one block; later ones are counted.
	detectMaxLines = 200
	// detectMaxBytes caps the bytes of one block.
	detectMaxBytes = 32 << 10
	// detectMaxLineBytes caps one stored line.
	detectMaxLineBytes = 4 << 10
	// detectMaxBlanks is how many blank lines a block may hold inside it
	// (Python chains and Go panics have blank lines between their parts).
	detectMaxBlanks = 2
	// DetectIdle is how long a stream must be quiet before Flush closes the
	// block it holds and gives the incident out without the full after
	// context.
	DetectIdle = 2 * time.Second
)

// Rules are a project's extra rules on top of the built-in ones. Include
// patterns start an incident on a line the built-in rules pass over; an
// incident with a line that matches an Exclude pattern is dropped whole.
// The patterns are RE2; domain.ValidateLadoLogWatchInput checks them before
// they are saved, and NewDetector skips one that does not compile.
type Rules struct {
	Include []string
	Exclude []string
}

// Incident is one error: an error line, or a stack trace gathered into one
// block, with the lines around it.
type Incident struct {
	// ExceptionKind is the exception class or error kind, as written in the
	// log ("java.lang.IllegalStateException", "KeyError", "TypeError",
	// "panic"), or the level word ("ERROR") for a plain error line. It is
	// log text: clean it with CleanKind before it goes into a title.
	ExceptionKind string
	// Location is the first application frame as written in the log, or
	// empty when the block has no frame. It is log text, like
	// ExceptionKind.
	Location string
	// Fingerprint is the hex sha256 of the service name, the exception kind
	// and the normalized first application frame (or the normalized message
	// when there is no frame).
	Fingerprint string
	// Lines are the block's lines, Before and After the lines around it.
	Lines  []string
	Before []string
	After  []string
	// At is the timestamp of the block's first line.
	At time.Time
	// Stream is the stream the block came on ("stdout", "stderr").
	Stream string
}

// Occurrence is one incident seen on one instance of a service: what the
// runner hands the incident handler.
type Occurrence struct {
	// Service is the service name the fingerprint was made with.
	Service string
	// Instance is the instance's display name, "<source>/<container>".
	Instance string
	// Image is the instance's image reference.
	Image string
	// ServiceInstances is how many instances of the service were attached
	// when the incident was seen, over every source.
	ServiceInstances int
	// Repository is the canonical id of the repository the occurrence's
	// issue goes to: its target's, or else the project's; empty when
	// neither names one.
	Repository string
	Incident   Incident
}

// Detector gathers one log stream's lines into incidents. Give each instance
// (container) its own Detector, made with the service's name: lines of two
// containers must not mix inside one block, and the shared service name makes
// their fingerprints the same. The stdout and stderr streams of one Detector
// are gathered apart. A Detector is safe for concurrent use.
type Detector struct {
	service string
	include []*regexp.Regexp
	exclude []*regexp.Regexp

	mu      sync.Mutex
	streams map[string]*detectStream
}

// detectStream is the state of one stream of one Detector.
type detectStream struct {
	before []string
	block  *detectBlock
	// pending is a closed block waiting for its after context.
	pending *detectBlock
	// blanks are blank lines held inside the open block until a line shows
	// whether the block goes on.
	blanks int
	// mark is the first Flush time after the stream's last line.
	mark    time.Time
	markSet bool
}

// detectBlock is one block being gathered.
type detectBlock struct {
	lines   []string
	before  []string
	after   []string
	bytes   int
	dropped int
	at      time.Time
	stream  string
	json    bool
	// frames is set once a stack frame line joined the block.
	frames bool
	// py is the Python traceback state: 0 none, 1 inside a traceback,
	// 2 after its exception line. chain is set by a chain phrase.
	py    int
	chain bool
	// golang is set for a Go panic or fatal error.
	golang bool
}

var (
	detectANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// detectLevel finds an error level word in a text line.
	detectLevel = regexp.MustCompile(`(?:^|[\s\[|:/(<])(ERROR|FATAL|CRITICAL|PANIC|CRIT|SEVERE)(?:$|[\s\]|:/)>])`)
	// detectLevelLower finds bracketed lower-case levels (nginx) and logfmt
	// levels.
	detectLevelLower = regexp.MustCompile(`(?i)\[(?:error|crit|alert|emerg|fatal)\]|\blevel=["']?(error|err|fatal|critical|crit|panic)\b`)
	detectPyStart    = regexp.MustCompile(`^Traceback \(most recent call last\):`)
	detectPyChain    = regexp.MustCompile(`^(?:During handling of the above exception, another exception occurred:|The above exception was the direct cause of the following exception:)`)
	detectGoStart    = regexp.MustCompile(`^(?:panic: |fatal error: )`)
	detectGoLine     = regexp.MustCompile(`^(?:goroutine \d+ \[|\[signal |created by |panic: |exit status \d+$|\S+\(.*\)$)`)
	// detectJavaHeader is a Java exception line on its own.
	detectJavaHeader = regexp.MustCompile(`^(?:Exception in thread "[^"]*" )?(?:[a-zA-Z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error|Throwable)(?::.*)?$`)
	detectJavaMore   = regexp.MustCompile(`^\s*(?:Caused by: |Suppressed: |\.\.\. \d+ (?:more|common frames omitted))`)
	// detectJavaClass finds a Java exception class anywhere in a line.
	detectJavaClass = regexp.MustCompile(`(?:^|[\s:"(])((?:[a-zA-Z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error|Throwable))\b`)
	// detectNodeHeader is a JavaScript error line ("TypeError: …").
	detectNodeHeader = regexp.MustCompile(`^(?:Uncaught )?((?:[A-Z][\w$]*)?(?:Error|Exception))(?: \[[A-Z0-9_]+\])?:`)
	detectPyExc      = regexp.MustCompile(`^([A-Za-z_][\w.]*)(?::|$)`)
	detectAtFrame    = regexp.MustCompile(`^\s+at\s+(\S.*)$`)
	detectJavaFrame  = regexp.MustCompile(`^[^\s(]+\(.*\)$`)
	detectPyFrame    = regexp.MustCompile(`^\s+File "([^"]+)", line (\d+)(?:, in (.+))?`)
	detectGoFile     = regexp.MustCompile(`^\t(\S+?):(\d+)`)
	detectGoFunc     = regexp.MustCompile(`^\S+\(.*\)$`)

	detectNormTS     = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	detectNormUUID   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	detectNorm0x     = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	detectNormHex    = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	detectNormID     = regexp.MustCompile(`\b[A-Za-z0-9_-]{20,}\b`)
	detectNormDigits = regexp.MustCompile(`\d+`)
)

// NewDetector returns a Detector for one instance of the named service.
func NewDetector(service string, rules Rules) *Detector {
	return &Detector{
		service: service,
		include: detectCompile(rules.Include),
		exclude: detectCompile(rules.Exclude),
		streams: map[string]*detectStream{},
	}
}

func detectCompile(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

// Line takes one log line, without its Docker timestamp, and returns the
// incidents it completed: usually none, since a block is given out once its
// after context is in or a new incident starts.
func (d *Detector) Line(stream string, ts time.Time, text string) []Incident {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.streams[stream]
	if s == nil {
		s = &detectStream{}
		d.streams[stream] = s
	}
	s.markSet = false
	text = detectClip(detectANSI.ReplaceAllString(strings.TrimRight(text, "\r\n"), ""))

	var out []Incident
	if b := s.block; b != nil {
		switch {
		case strings.TrimSpace(text) == "" && !b.json && s.blanks < detectMaxBlanks:
			s.blanks++
			return nil
		case strings.TrimSpace(text) != "" && detectContinues(b, text):
			for ; s.blanks > 0; s.blanks-- {
				b.add("")
			}
			b.add(text)
			return nil
		}
		s.blanks = 0
		s.pending, s.block = b, nil
	}
	if detectStarts(d.include, text) {
		if s.pending != nil {
			out = append(out, d.emit(s.pending)...)
			s.pending = nil
		}
		s.block = &detectBlock{
			before: append([]string(nil), s.before...),
			at:     ts,
			stream: stream,
		}
		_, s.block.json = detectJSON(strings.TrimSpace(text))
		s.block.golang = detectGoStart.MatchString(text)
		if detectPyStart.MatchString(text) {
			s.block.py = 1
		}
		s.block.add(text)
		return out
	}
	if p := s.pending; p != nil {
		p.after = append(p.after, text)
		if len(p.after) >= detectAfterLines {
			out = append(out, d.emit(p)...)
			s.pending = nil
		}
	}
	s.before = append(s.before, text)
	if len(s.before) > detectBeforeLines {
		s.before = s.before[len(s.before)-detectBeforeLines:]
	}
	return out
}

// Flush gives out what has waited long enough. The first Flush after a
// stream's last line marks the time; a Flush DetectIdle or more after that
// mark closes the stream's open block and gives out its incident with the
// after context it has. Call it on a steady tick with one clock.
func (d *Detector) Flush(now time.Time) []Incident {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Incident
	for _, s := range d.streams {
		if s.block == nil && s.pending == nil {
			continue
		}
		if !s.markSet {
			s.mark, s.markSet = now, true
			continue
		}
		if now.Sub(s.mark) < DetectIdle {
			continue
		}
		if s.block != nil {
			// A pending block is given out when the next one starts, so an
			// open block never has one beside it.
			s.pending, s.block, s.blanks = s.block, nil, 0
		}
		out = append(out, d.emit(s.pending)...)
		s.pending = nil
	}
	return out
}

func (b *detectBlock) add(text string) {
	if len(b.lines) >= detectMaxLines || b.bytes+len(text) > detectMaxBytes {
		b.dropped++
		return
	}
	b.lines = append(b.lines, text)
	b.bytes += len(text) + 1
}

// detectStarts reports whether a line starts an incident.
func detectStarts(include []*regexp.Regexp, text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		if fields, ok := detectJSON(trimmed); ok {
			return detectJSONError(fields)
		}
	}
	if detectPyStart.MatchString(text) || detectGoStart.MatchString(text) ||
		detectJavaHeader.MatchString(text) || detectNodeHeader.MatchString(text) ||
		detectLevel.MatchString(text) || detectLevelLower.MatchString(text) {
		return true
	}
	for _, re := range include {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// detectContinues reports whether a non-blank line belongs to the open
// block, and updates the block's state for it.
func detectContinues(b *detectBlock, text string) bool {
	if b.json {
		return false
	}
	indented := text[0] == ' ' || text[0] == '\t'
	if indented && (detectAtFrame.MatchString(text) || detectPyFrame.MatchString(text) || (b.golang && text[0] == '\t')) {
		b.frames = true
	}
	switch {
	case indented, detectJavaMore.MatchString(text):
		return true
	case b.golang:
		return detectGoLine.MatchString(text)
	case detectPyStart.MatchString(text):
		// A traceback joins the error line that announced it, or the
		// chain phrase before it; another traceback is a new incident.
		if (b.py == 0 && len(b.lines) == 1) || b.chain {
			b.py, b.chain = 1, false
			return true
		}
		return false
	case b.py == 2 && detectPyChain.MatchString(text):
		b.chain = true
		return true
	case b.py == 1 && b.frames:
		// The first line after the frames that is not indented is the
		// exception.
		b.py = 2
		return true
	case !b.frames && b.py == 0 && (detectJavaHeader.MatchString(text) || detectNodeHeader.MatchString(text)):
		// The exception line under the error line that logged it.
		return true
	}
	return false
}

// emit turns a closed block into its incident, or into nothing when an
// exclude rule matches one of its lines.
func (d *Detector) emit(b *detectBlock) []Incident {
	for _, re := range d.exclude {
		for _, line := range b.lines {
			if re.MatchString(line) {
				return nil
			}
		}
	}
	lines := append([]string(nil), b.lines...)
	if b.dropped > 0 {
		lines = append(lines, fmt.Sprintf("… %d줄 생략", b.dropped))
	}
	kind, location, key := detectAnalyze(b.lines, b.json)
	return []Incident{{
		ExceptionKind: kind,
		Location:      location,
		Fingerprint:   detectFingerprint(d.service, kind, key),
		Lines:         lines,
		Before:        b.before,
		After:         append([]string(nil), b.after...),
		At:            b.at,
		Stream:        b.stream,
	}}
}

// detectFingerprint is sha256(service, kind, key) in hex. The parts are
// joined with a newline, which none of them holds.
func detectFingerprint(service, kind, key string) string {
	sum := sha256.Sum256([]byte(service + "\n" + kind + "\n" + key))
	return hex.EncodeToString(sum[:])
}

// detectFrame is one stack frame.
type detectFrame struct {
	text string
	lib  bool
}

// detectAnalyze reads a block's exception kind, its first application frame
// and the key its fingerprint is made from.
func detectAnalyze(lines []string, isJSON bool) (kind, location, key string) {
	message := ""
	if len(lines) > 0 {
		message = lines[0]
	}
	jsonKind := ""
	if isJSON && len(lines) > 0 {
		if fields, ok := detectJSON(strings.TrimSpace(lines[0])); ok {
			info := detectJSONInfo(fields)
			jsonKind = info.kind
			if jsonKind == "" && info.stack == "" {
				jsonKind = info.level
			}
			if info.message != "" {
				message = info.message
			}
			if info.stack != "" {
				lines = strings.Split(strings.ReplaceAll(info.stack, "\r\n", "\n"), "\n")
			} else {
				lines = nil
			}
		}
	}

	frames, python := detectFrames(lines)
	kind = jsonKind
	if kind == "" {
		kind = detectKind(lines, message)
	}
	var frame detectFrame
	if python {
		// A Python traceback lists the innermost call last.
		for i := len(frames) - 1; i >= 0; i-- {
			if !frames[i].lib {
				frame = frames[i]
				break
			}
		}
		if frame.text == "" && len(frames) > 0 {
			frame = frames[len(frames)-1]
		}
	} else {
		for _, f := range frames {
			if !f.lib {
				frame = f
				break
			}
		}
		if frame.text == "" && len(frames) > 0 {
			frame = frames[0]
		}
	}
	if frame.text != "" {
		return kind, frame.text, detectNormalize(frame.text)
	}
	return kind, "", detectNormalize(message)
}

// detectKind reads the exception kind from a block's lines.
func detectKind(lines []string, message string) string {
	pyKind, inTrace := "", false
	for _, line := range lines {
		switch {
		case detectPyStart.MatchString(line):
			inTrace = true
		case inTrace && line != "" && line[0] != ' ' && line[0] != '\t' && !detectPyChain.MatchString(line):
			if m := detectPyExc.FindStringSubmatch(line); m != nil {
				pyKind = m[1]
			}
		}
	}
	if pyKind != "" {
		return pyKind
	}
	for _, line := range lines {
		if detectGoStart.MatchString(line) {
			if strings.HasPrefix(line, "panic: ") {
				return "panic"
			}
			return "fatal error"
		}
	}
	for _, line := range lines {
		if detectAtFrame.MatchString(line) {
			continue
		}
		if m := detectJavaClass.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	for _, line := range lines {
		if m := detectNodeHeader.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	if m := detectLevel.FindStringSubmatch(message); m != nil {
		return m[1]
	}
	if m := detectLevelLower.FindStringSubmatch(message); len(m) > 1 && m[1] != "" {
		return strings.ToUpper(m[1])
	}
	return "ERROR"
}

// detectFrames reads a block's stack frames in order, and whether they are
// Python frames.
func detectFrames(lines []string) (frames []detectFrame, python bool) {
	for i, line := range lines {
		if m := detectPyFrame.FindStringSubmatch(line); m != nil {
			text := m[1] + ":" + m[2]
			if m[3] != "" {
				text += " in " + strings.TrimSpace(m[3])
			}
			frames = append(frames, detectFrame{text: text, lib: detectPyLib(m[1])})
			python = true
			continue
		}
		if m := detectAtFrame.FindStringSubmatch(line); m != nil {
			text := strings.TrimSpace(m[1])
			if detectJavaFrame.MatchString(text) {
				frames = append(frames, detectJavaFrameOf(text))
			} else {
				frames = append(frames, detectFrame{text: text, lib: detectNodeLib(text)})
			}
			continue
		}
		if i == 0 {
			continue
		}
		prev := lines[i-1]
		if m := detectGoFile.FindStringSubmatch(line); m != nil && (detectGoFunc.MatchString(prev) || strings.HasPrefix(prev, "created by ")) {
			fn := strings.TrimSpace(prev)
			fn = strings.TrimPrefix(fn, "created by ")
			if j := strings.LastIndex(fn, "("); j > 0 && strings.HasSuffix(fn, ")") {
				fn = fn[:j]
			}
			if j := strings.Index(fn, " in goroutine "); j > 0 {
				fn = fn[:j]
			}
			frames = append(frames, detectFrame{
				text: fn + " " + m[1] + ":" + m[2],
				lib:  detectGoLib(fn, m[1]),
			})
		}
	}
	return frames, python
}

// detectJavaFrameOf reads a Java frame, dropping its module prefix
// ("java.base/", "app//").
func detectJavaFrameOf(text string) detectFrame {
	method := text
	if paren := strings.Index(text, "("); paren >= 0 {
		method = text[:paren]
	}
	if j := strings.LastIndex(method, "/"); j >= 0 {
		text = text[j+1:]
		method = method[j+1:]
	}
	return detectFrame{text: text, lib: detectJavaLib(method)}
}

var detectJavaLibPrefixes = []string{
	"java.", "javax.", "jakarta.", "sun.", "com.sun.", "jdk.", "kotlin.", "kotlinx.",
	"scala.", "groovy.", "org.codehaus.groovy.", "org.springframework.", "org.apache.",
	"io.netty.", "reactor.", "org.hibernate.", "com.fasterxml.", "org.eclipse.jetty.",
	"io.undertow.", "com.zaxxer.", "org.junit.",
}

func detectJavaLib(method string) bool {
	for _, p := range detectJavaLibPrefixes {
		if strings.HasPrefix(method, p) {
			return true
		}
	}
	return false
}

func detectNodeLib(text string) bool {
	return strings.Contains(text, "node:") || strings.Contains(text, "node_modules/") ||
		strings.Contains(text, "(internal/") || strings.HasPrefix(text, "internal/") ||
		text == "<anonymous>"
}

func detectPyLib(path string) bool {
	for _, p := range []string{"site-packages/", "dist-packages/", "/usr/lib/python", "/usr/local/lib/python", "/lib/python3", "<frozen "} {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

func detectGoLib(fn, file string) bool {
	if strings.HasPrefix(fn, "runtime.") || strings.HasPrefix(fn, "panic") {
		return true
	}
	for _, p := range []string{"/usr/local/go/", "/usr/lib/go", "/src/runtime/", "/pkg/mod/", "/vendor/", "GOROOT", "@v"} {
		if strings.Contains(file, p) {
			return true
		}
	}
	return false
}

// detectNormalize drops what changes between two occurrences of one error:
// times, UUIDs, hex and long ids, and numbers (frame line numbers too).
func detectNormalize(s string) string {
	s = detectNormTS.ReplaceAllString(s, "<ts>")
	s = detectNormUUID.ReplaceAllString(s, "<uuid>")
	s = detectNorm0x.ReplaceAllString(s, "<hex>")
	s = detectNormHex.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") {
			return "<hex>"
		}
		return m
	})
	s = detectNormID.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") && strings.IndexFunc(m, func(r rune) bool {
			return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		}) >= 0 {
			return "<id>"
		}
		return m
	})
	s = detectNormDigits.ReplaceAllString(s, "0")
	return strings.Join(strings.Fields(s), " ")
}

// detectClip cuts a line to detectMaxLineBytes on a rune boundary.
func detectClip(s string) string {
	if len(s) <= detectMaxLineBytes {
		return s
	}
	cut := detectMaxLineBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
