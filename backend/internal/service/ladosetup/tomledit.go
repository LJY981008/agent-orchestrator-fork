package ladosetup

// Line edits of Codex's config.toml (plan §7.5). Adoption cuts a
// [mcp_servers.<name>] table and its [mcp_servers.<name>.<sub>] tables out
// line by line, and restore appends the cut text at the end. Every other
// line, comments included, stays as it is. Each edit is checked by parsing:
// the result must hold exactly the values it should, so a header look-alike
// inside a multi-line string fails the edit instead of corrupting the file.

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// splitLines splits data into lines that keep their line endings.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			lines = append(lines, data)
			break
		}
		lines = append(lines, data[:i+1])
		data = data[i+1:]
	}
	return lines
}

// tomlHeader reads a table header line ("[a.b]" or "[[a.b]]", spaces and a
// comment allowed) and returns its dotted key. ok is false for any other
// line.
func tomlHeader(line []byte) (keys []string, array, ok bool) {
	s := strings.TrimRight(string(line), "\r\n")
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, "[") {
		return nil, false, false
	}
	s, array = s[1:], strings.HasPrefix(s, "[[")
	if array {
		s = s[1:]
	}
	for {
		s = strings.TrimLeft(s, " \t")
		key, rest, ok := tomlKey(s)
		if !ok {
			return nil, false, false
		}
		keys = append(keys, key)
		s = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(s, ".") {
			s = s[1:]
			continue
		}
		break
	}
	closing := "]"
	if array {
		closing = "]]"
	}
	if !strings.HasPrefix(s, closing) {
		return nil, false, false
	}
	s = strings.TrimLeft(s[len(closing):], " \t")
	if s != "" && !strings.HasPrefix(s, "#") {
		return nil, false, false
	}
	return keys, array, true
}

// tomlKey reads one key at the start of s: bare, "basic" or 'literal'.
func tomlKey(s string) (key, rest string, ok bool) {
	switch {
	case strings.HasPrefix(s, `"`):
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				key, err := strconv.Unquote(s[:i+1])
				return key, s[i+1:], err == nil
			}
		}
		return "", "", false
	case strings.HasPrefix(s, "'"):
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "", false
		}
		return s[1 : end+1], s[end+2:], true
	}
	n := 0
	for n < len(s) && isBareKeyByte(s[n]) {
		n++
	}
	return s[:n], s[n:], n > 0
}

func isBareKeyByte(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9') || b == '_' || b == '-'
}

// isTOMLComment reports whether a line holds only a comment.
func isTOMLComment(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("#"))
}

func isBlank(line []byte) bool { return len(bytes.TrimSpace(line)) == 0 }

// tomlWithoutServer cuts the MCP server name out of config.toml: its
// [mcp_servers.<name>] table and every [mcp_servers.<name>.<sub>] table, each
// from its header line to the next header. Comment lines right above the
// next header belong to that table and stay; at the end of the file, the
// blank lines before the cut go too. It returns the rest of the file and the
// cut text, which restore appends to put the server back.
func tomlWithoutServer(data []byte, name string) (rest, block []byte, err error) {
	lines := splitLines(data)
	cut := make([]bool, len(lines))
	found := false
	for i := 0; i < len(lines); i++ {
		keys, array, ok := tomlHeader(lines[i])
		if !ok || len(keys) < 2 || keys[0] != codexServersKey || keys[1] != name || (len(keys) == 2 && array) {
			continue
		}
		found = found || len(keys) == 2
		next := i + 1
		for next < len(lines) {
			if _, _, ok := tomlHeader(lines[next]); ok {
				break
			}
			next++
		}
		end := next
		for end > i+1 && isTOMLComment(lines[end-1]) {
			end--
		}
		if end == len(lines) {
			for i > 0 && isBlank(lines[i-1]) && !cut[i-1] {
				i--
			}
		}
		for j := i; j < end; j++ {
			cut[j] = true
		}
		i = next - 1
	}
	if !found {
		return nil, nil, fmt.Errorf("config.toml has no [%s.%s] table", codexServersKey, name)
	}
	var kept, removed bytes.Buffer
	for i, line := range lines {
		if !cut[i] {
			kept.Write(line)
			continue
		}
		if removed.Len() == 0 && isBlank(line) {
			continue
		}
		removed.Write(line)
	}
	rest, block = kept.Bytes(), removed.Bytes()
	if len(block) > 0 && !bytes.HasSuffix(block, []byte("\n")) {
		block = append(block, '\n')
	}
	if err := checkServerCut(data, rest, block, name); err != nil {
		return nil, nil, err
	}
	return rest, block, nil
}

// tomlWithBlock appends a cut server block at the end of config.toml, after a
// blank line. The result must parse and hold the block's server as the block
// does: a server of that name already in data fails.
func tomlWithBlock(data, block []byte) ([]byte, error) {
	out := bytes.Clone(data)
	if len(bytes.TrimSpace(out)) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		if !bytes.HasSuffix(out, []byte("\n\n")) {
			out = append(out, '\n')
		}
	}
	out = append(out, block...)
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	var before, added, after map[string]any
	if err := toml.Unmarshal(data, &before); err != nil {
		return nil, fmt.Errorf("read config.toml: %w", err)
	}
	if err := toml.Unmarshal(block, &added); err != nil {
		return nil, fmt.Errorf("read the server block: %w", err)
	}
	if err := toml.Unmarshal(out, &after); err != nil {
		return nil, fmt.Errorf("config.toml with the server block does not parse: %w", err)
	}
	servers, _ := added[codexServersKey].(map[string]any)
	if len(added) != 1 || len(servers) != 1 {
		return nil, errors.New("the server block does not hold exactly one MCP server")
	}
	if before == nil {
		before = map[string]any{}
	}
	merged, _ := before[codexServersKey].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	for name, table := range servers {
		if _, taken := merged[name]; taken {
			return nil, fmt.Errorf("config.toml already has the MCP server %s", name)
		}
		merged[name] = table
	}
	before[codexServersKey] = merged
	if !reflect.DeepEqual(before, after) {
		return nil, errors.New("adding the server block changes other values of config.toml")
	}
	return out, nil
}

// checkServerCut checks a cut by parsing: rest must hold the original's
// values without the server, and block the server alone.
func checkServerCut(data, rest, block []byte, name string) error {
	var before, after, cut map[string]any
	if err := toml.Unmarshal(data, &before); err != nil {
		return fmt.Errorf("read config.toml: %w", err)
	}
	if err := toml.Unmarshal(rest, &after); err != nil {
		return fmt.Errorf("config.toml without the server does not parse: %w", err)
	}
	if err := toml.Unmarshal(block, &cut); err != nil {
		return fmt.Errorf("the server's lines do not parse alone: %w", err)
	}
	servers, _ := before[codexServersKey].(map[string]any)
	table, ok := servers[name]
	if !ok {
		return fmt.Errorf("config.toml has no MCP server %s", name)
	}
	cutServers, _ := cut[codexServersKey].(map[string]any)
	if len(cut) != 1 || len(cutServers) != 1 || !reflect.DeepEqual(cutServers[name], table) {
		return fmt.Errorf("the lines cut for %s do not hold exactly that server", name)
	}
	delete(servers, name)
	dropEmptyServers(before)
	dropEmptyServers(after)
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("cutting %s out changes other values of config.toml", name)
	}
	return nil
}

// dropEmptyServers removes an mcp_servers table without servers, which a
// file without server tables does not have.
func dropEmptyServers(doc map[string]any) {
	if servers, ok := doc[codexServersKey].(map[string]any); ok && len(servers) == 0 {
		delete(doc, codexServersKey)
	}
}
