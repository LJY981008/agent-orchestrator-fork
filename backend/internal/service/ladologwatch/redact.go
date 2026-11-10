package ladologwatch

import (
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// The marks Redact leaves where it took something out.
const (
	redactSecret = "[REDACTED]"
	redactEmail  = "[EMAIL]"
	redactPhone  = "[PHONE]"
	redactCard   = "[CARD]"
	redactIP     = "[IP]"
	redactHost   = "[HOST]"
)

var (
	// redactHeader masks an Authorization or Cookie header's value, quoted
	// (JSON) or to the end of the line.
	redactHeader = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|set-cookie|cookie)("?[ \t]*[:=][ \t]*)("(?:[^"\\\r\n]|\\.)*"|[^\r\n]*)`)
	redactBearer = regexp.MustCompile(`(?i)\b(bearer|basic|token)[ \t]+[A-Za-z0-9._~+/=-]{8,}`)
	redactJWT    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	// redactToken masks tokens by their well-known prefixes.
	redactToken = regexp.MustCompile(`\b(?:glpat-|gl(?:dt|rt|ptt|cbt|imt|oas|soat|ft|agent)-|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|xox[abprs]-|sk-(?:live|test|proj|ant)?[-_]?|AKIA|ASIA|AIza)[A-Za-z0-9_\-]{8,}`)
	// redactKeyValue masks the value of a secret-looking key.
	redactKeyValue = regexp.MustCompile(`(?i)\b((?:[\w-]*(?:password|passwd|pwd|secret|token|api[_-]?key|apikey|private[_-]?key|credential|session[_-]?id|sessionid))|pass|auth)("?'?[ \t]*[:=][ \t]*)("(?:[^"\\\r\n]|\\.)*"|'[^'\r\n]*'|[^\s,;&"'}\[\]][^\s,;&"'}\]]*)`)
	// redactURLUser masks the user part of scheme://user:pass@host.
	redactURLUser = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@"'<>]+@`)
	// redactBareUser masks user:pass@host written without a scheme.
	redactBareUser = regexp.MustCompile(`(^|[\s"'=(,])[A-Za-z0-9._%+-]+:[^\s@/"']+@[A-Za-z0-9.-]+`)
	redactURLHost  = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://(?:[^/\s@"'<>]*@)?)(\[[0-9a-f:.]+\]|[^/\s:?#"'<>\[\]@]+)`)
	redactEmailRe  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	redactIPv4     = regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b`)
	// redactIPv6Candidate finds runs that could be IPv6 addresses; the
	// parser decides.
	redactIPv6Candidate = regexp.MustCompile(`[0-9A-Fa-f]*:[0-9A-Fa-f]*:[0-9A-Fa-f:.]*`)
	redactCardRe        = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	redactPhoneRe       = regexp.MustCompile(`(?:\+\d{1,3}[ .-]?)?\(?\b\d{2,4}\)?[ .-]\d{3,4}[ .-]\d{4}\b|\b01[016789]\d{7,8}\b`)
)

// Redact masks secrets, personal data and hosts in log text before it goes
// to a tracker: Authorization and Cookie headers, bearer and well-known
// tokens, JWTs, password-like key values, the user part of connection
// strings, URL hosts, email addresses, Luhn-valid card numbers, phone
// numbers, IP addresses, and each of extraHosts (the source endpoints' and
// image registries' hosts). It is applied once more to a whole issue title
// and body just before they are sent.
func Redact(text string, extraHosts []string) string {
	text = redactHeader.ReplaceAllStringFunc(text, func(m string) string {
		parts := redactHeader.FindStringSubmatch(m)
		if strings.HasPrefix(parts[3], `"`) {
			return parts[1] + parts[2] + `"` + redactSecret + `"`
		}
		return parts[1] + parts[2] + redactSecret
	})
	text = redactBearer.ReplaceAllString(text, "$1 "+redactSecret)
	text = redactJWT.ReplaceAllString(text, redactSecret)
	text = redactToken.ReplaceAllString(text, redactSecret)
	text = redactKeyValue.ReplaceAllStringFunc(text, func(m string) string {
		parts := redactKeyValue.FindStringSubmatch(m)
		if parts[3] == `"`+redactSecret+`"` {
			return m
		}
		if strings.HasPrefix(parts[3], `"`) {
			return parts[1] + parts[2] + `"` + redactSecret + `"`
		}
		return parts[1] + parts[2] + redactSecret
	})
	text = redactURLUser.ReplaceAllString(text, "${1}"+redactSecret+"@")
	text = redactBareUser.ReplaceAllString(text, "${1}"+redactSecret+"@"+redactHost)
	text = redactURLHost.ReplaceAllString(text, "${1}"+redactHost)
	text = redactHosts(text, extraHosts)
	text = redactEmailRe.ReplaceAllString(text, redactEmail)
	text = redactIPv4.ReplaceAllString(text, redactIP)
	text = redactIPv6s(text)
	text = redactCardRe.ReplaceAllStringFunc(text, func(m string) string {
		if redactLuhn(m) {
			return redactCard
		}
		return m
	})
	return redactPhoneRe.ReplaceAllString(text, redactPhone)
}

// redactHosts masks each host, the longest first so that a host is not
// half masked by a shorter one inside it.
func redactHosts(text string, hosts []string) string {
	list := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.Trim(strings.TrimSpace(h), "[]")
		if len(h) >= 2 {
			list = append(list, h)
		}
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, h := range list {
		re := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.-])` + regexp.QuoteMeta(h) + `($|[^A-Za-z0-9-])`)
		// The pattern takes the character on each side, so two hosts one
		// character apart need a second pass.
		for range 2 {
			text = re.ReplaceAllString(text, "${1}"+redactHost+"${2}")
		}
	}
	return text
}

// redactIPv6s masks each run that parses as an IPv6 address and does not
// sit inside a word ("self::create" is not an address).
func redactIPv6s(text string) string {
	var b strings.Builder
	last := 0
	for _, loc := range redactIPv6Candidate.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if start > 0 && redactWordByte(text[start-1]) || end < len(text) && redactWordByte(text[end]) {
			continue
		}
		m := text[start:end]
		core := strings.TrimRight(m, ":.")
		addr, err := netip.ParseAddr(core)
		if err != nil || !addr.Is6() {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(redactIP)
		b.WriteString(m[len(core):])
		last = end
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

func redactWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// redactLuhn reports whether a run of 13 to 19 digits, spaces and dashes
// passes the Luhn check.
func redactLuhn(s string) bool {
	digits := make([]int, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	for i := range digits {
		d := digits[len(digits)-1-i]
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}
