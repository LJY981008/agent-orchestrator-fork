package ladologwatch

import (
	"encoding/json"
	"strconv"
	"strings"
)

// detectJSONLevels are the level values that make a JSON log line an error.
var detectJSONLevels = map[string]string{
	"error": "ERROR", "err": "ERROR", "severe": "ERROR",
	"fatal": "FATAL", "critical": "CRITICAL", "crit": "CRITICAL",
	"panic": "PANIC", "emerg": "FATAL", "emergency": "FATAL", "alert": "FATAL",
}

// detectJSON parses a line that is one JSON object.
func detectJSON(line string) (map[string]any, bool) {
	if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
		return nil, false
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return nil, false
	}
	return fields, true
}

// detectJSONField reads a field by its name, a dotted name written as one
// key ("error.type") or a path through nested objects.
func detectJSONField(fields map[string]any, name string) (any, bool) {
	if v, ok := fields[name]; ok {
		return v, true
	}
	head, rest, found := strings.Cut(name, ".")
	if !found {
		return nil, false
	}
	nested, ok := fields[head].(map[string]any)
	if !ok {
		return nil, false
	}
	return detectJSONField(nested, rest)
}

func detectJSONString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if v, ok := detectJSONField(fields, name); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// detectJSONLevel reads a JSON line's level as an upper-case error level
// word, or "" when the line is not an error.
func detectJSONLevel(fields map[string]any) string {
	for _, name := range []string{"level", "lvl", "severity", "levelname", "log.level", "@l", "loglevel"} {
		v, ok := detectJSONField(fields, name)
		if !ok {
			continue
		}
		switch level := v.(type) {
		case string:
			if word, ok := detectJSONLevels[strings.ToLower(strings.TrimSpace(level))]; ok {
				return word
			}
			if n, err := strconv.Atoi(level); err == nil {
				return detectJSONNumericLevel(float64(n))
			}
			return ""
		case float64:
			return detectJSONNumericLevel(level)
		}
		return ""
	}
	return ""
}

// detectJSONNumericLevel reads pino and bunyan levels: 50 is error, 60 fatal.
func detectJSONNumericLevel(n float64) string {
	switch {
	case n >= 60:
		return "FATAL"
	case n >= 50:
		return "ERROR"
	}
	return ""
}

func detectJSONError(fields map[string]any) bool {
	return detectJSONLevel(fields) != ""
}

// detectJSONEntry is what a JSON error line holds.
type detectJSONEntry struct {
	level   string
	kind    string
	message string
	stack   string
}

func detectJSONInfo(fields map[string]any) detectJSONEntry {
	return detectJSONEntry{
		level: detectJSONLevel(fields),
		kind: detectJSONString(fields, "error.type", "error.kind", "error.name", "err.type", "err.name",
			"exception.type", "exception.class", "exc_type", "error_type", "exception_class"),
		message: detectJSONString(fields, "msg", "message", "@m", "error.message", "err.message"),
		stack: detectJSONString(fields, "stack", "stack_trace", "stacktrace", "exc_info", "trace",
			"error.stack", "error.stack_trace", "err.stack", "exception.stacktrace", "exception.stack_trace",
			"exception"),
	}
}
