package ladodbchange

// Redaction of what a SQL command prints and of the errors LADO keeps: the
// values of the repository's secrets, the passwords of connection strings and
// URL credentials never reach a stored output, a run's message, an audit
// event or a log line.

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

const (
	// Redacted is what stands where a secret was.
	Redacted = "[REDACTED]"
	// OutputKeepMax is how much of the end of a command's output LADO keeps.
	OutputKeepMax = 64 << 10
	// MessageMax caps a one-line message in characters.
	MessageMax = 300
	// secretRedactMin is the shortest secret value redacted by its value:
	// a shorter one would blank out ordinary words of the output.
	secretRedactMin = 4
)

var (
	// urlUserinfo matches the user information of a URL, as in
	// postgres://user:password@host, where a DSN carries its password.
	urlUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@"'<>]+@`)
	// passwordKeyValue matches a password in key=value form, as libpq's
	// "password=..." or an environment line "PGPASSWORD=...": quoted or up
	// to the next space, semicolon or ampersand.
	passwordKeyValue = regexp.MustCompile(`(?i)\b([a-z_]*(?:password|passwd|pwd))(\s*[=:]\s*)('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|[^\s;&'"]+)`)
	// tokenPattern matches the tokens the hosts issue, should a command
	// print one.
	tokenPattern = regexp.MustCompile(`(?i)\bbearer\s+\S+|\b(?:gh[pousr]_[0-9a-z]{20,}|github_pat_[0-9a-z_]{20,}|glpat-[0-9a-z_-]{20,})`)
)

// Redact replaces in text every secret value of at least four characters, as
// written and as a URL would escape it, and then the user information of
// URLs, password key values and host tokens, with [REDACTED].
func Redact(text string, secrets map[string]string) string {
	values := map[string]string{}
	for name, value := range secrets {
		if utf8.RuneCountInString(value) < secretRedactMin {
			continue
		}
		values[name] = value
		if escaped := url.QueryEscape(value); escaped != value {
			values[name+"\x00query"] = escaped
		}
		if escaped := url.PathEscape(value); escaped != value {
			values[name+"\x00path"] = escaped
		}
	}
	text = agentlaunch.RedactValues(text, values)
	text = redactURLCredentials(text)
	text = passwordKeyValue.ReplaceAllStringFunc(text, func(m string) string {
		parts := passwordKeyValue.FindStringSubmatch(m)
		if strings.Trim(parts[3], `'"`) == Redacted {
			return m
		}
		return parts[1] + parts[2] + Redacted
	})
	return tokenPattern.ReplaceAllString(text, Redacted)
}

// RedactOutput redacts a command's output (Redact) and keeps its last
// OutputKeepMax bytes, cut at a character's start. Redaction comes first, so
// a secret the cut would split is gone already.
func RedactOutput(output []byte, secrets map[string]string) []byte {
	text := Redact(strings.ToValidUTF8(string(output), "�"), secrets)
	if len(text) > OutputKeepMax {
		cut := len(text) - OutputKeepMax
		for cut < len(text) && !utf8.RuneStart(text[cut]) {
			cut++
		}
		text = text[cut:]
	}
	return []byte(text)
}

// RedactLine is text redacted (Redact) on one line, its spaces collapsed and
// cut to MessageMax characters: a run's error or a file's message.
func RedactLine(text string, secrets map[string]string) string {
	line := Redact(strings.Join(strings.Fields(text), " "), secrets)
	if utf8.RuneCountInString(line) <= MessageMax {
		return line
	}
	return string([]rune(line)[:MessageMax]) + "…"
}

// redactURLCredentials replaces the user information of URLs.
func redactURLCredentials(text string) string {
	return urlUserinfo.ReplaceAllString(text, "${1}"+Redacted+"@")
}

// cutBytes cuts text to at most limit bytes at a character's start.
func cutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
