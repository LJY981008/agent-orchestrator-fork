package ladosetup

// Order-keeping edits of a JSON object file (plan §7.5). Adoption takes the
// adopted keys out of Claude's settings.json and ~/.claude.json, and restore
// puts them back, without moving what stays. The file is written the way
// Claude writes it: two-space indented, each value's bytes re-indented as
// they were written (numbers and escapes kept), and the final newline kept.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// jsonMember is one member of a JSON object, with its value as written.
type jsonMember struct {
	Key   string
	Value json.RawMessage
}

// jsonMembers reads the members of the JSON object in data, in order. Empty
// input and null are an object without members.
func jsonMembers(data []byte) ([]jsonMember, error) {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var members []jsonMember
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		// The decoder reads only strings as object keys.
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, jsonMember{Key: key, Value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one JSON value")
	}
	return members, nil
}

// formatJSONObject writes members as an object indented by two spaces, each
// value re-indented, with a final newline when newline is set.
func formatJSONObject(members []jsonMember, newline bool) ([]byte, error) {
	var buf bytes.Buffer
	if len(members) == 0 {
		buf.WriteString("{}")
	} else {
		buf.WriteString("{\n")
		for i, m := range members {
			key, err := marshalCanonical(m.Key)
			if err != nil {
				return nil, err
			}
			buf.WriteString("  ")
			buf.Write(key)
			buf.WriteString(": ")
			if err := json.Indent(&buf, m.Value, "  ", "  "); err != nil {
				return nil, fmt.Errorf("the value of %q: %w", m.Key, err)
			}
			if i < len(members)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteByte('}')
	}
	if newline {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// jsonWithout returns the object in data without the top-level keys. The
// other members keep their order and values.
func jsonWithout(data []byte, keys ...string) ([]byte, error) {
	members, err := jsonMembers(data)
	if err != nil {
		return nil, err
	}
	members = slices.DeleteFunc(members, func(m jsonMember) bool { return slices.Contains(keys, m.Key) })
	return formatJSONObject(members, bytes.HasSuffix(data, []byte("\n")))
}

// jsonWith returns the object in data with key set to value: in its place
// when data has the key, after the other members when it has not.
func jsonWith(data []byte, key string, value []byte) ([]byte, error) {
	members, err := jsonMembers(data)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(members, func(m jsonMember) bool { return m.Key == key })
	if i < 0 {
		members = append(members, jsonMember{Key: key})
		i = len(members) - 1
	}
	members[i].Value = value
	return formatJSONObject(members, len(data) == 0 || bytes.HasSuffix(data, []byte("\n")))
}

// jsonMemberValue returns the value of key in the object in data, and
// whether data has it.
func jsonMemberValue(data []byte, key string) (json.RawMessage, bool, error) {
	members, err := jsonMembers(data)
	if err != nil {
		return nil, false, err
	}
	for _, m := range members {
		if m.Key == key {
			return m.Value, true, nil
		}
	}
	return nil, false, nil
}

// jsonWithoutMCPServer returns ~/.claude.json, or LADO's mcp.json, without
// the MCP server name. The mcpServers object stays, empty or not.
func jsonWithoutMCPServer(data []byte, name string) ([]byte, error) {
	servers, ok, err := jsonMemberValue(data, claudeMCPKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no %s in the object", claudeMCPKey)
	}
	servers, err = jsonWithout(servers, name)
	if err != nil {
		return nil, fmt.Errorf("the value of %s: %w", claudeMCPKey, err)
	}
	return jsonWith(data, claudeMCPKey, servers)
}

// jsonWithMCPServer returns data with the MCP server name set to value in
// mcpServers, which it creates when data has none.
func jsonWithMCPServer(data []byte, name string, value []byte) ([]byte, error) {
	servers, _, err := jsonMemberValue(data, claudeMCPKey)
	if err != nil {
		return nil, err
	}
	servers, err = jsonWith(servers, name, value)
	if err != nil {
		return nil, fmt.Errorf("the value of %s: %w", claudeMCPKey, err)
	}
	return jsonWith(data, claudeMCPKey, bytes.TrimSuffix(servers, []byte("\n")))
}

// memberKeys returns the keys of the object in data in order, or nil when
// data holds no object.
func memberKeys(data []byte) []string {
	members, err := jsonMembers(data)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(members))
	for _, m := range members {
		keys = append(keys, m.Key)
	}
	return keys
}

// insertMember inserts m among members where order, the keys of another
// version of the object, puts it: right after the nearest key before it in
// order that members have, else right before the nearest one after it, else
// at the end.
func insertMember(members []jsonMember, m jsonMember, order []string) []jsonMember {
	index := func(key string) int {
		return slices.IndexFunc(members, func(mm jsonMember) bool { return mm.Key == key })
	}
	at := len(members)
	if pos := slices.Index(order, m.Key); pos >= 0 {
		placed := false
		for k := pos - 1; k >= 0 && !placed; k-- {
			if j := index(order[k]); j >= 0 {
				at, placed = j+1, true
			}
		}
		for k := pos + 1; k < len(order) && !placed; k++ {
			if j := index(order[k]); j >= 0 {
				at, placed = j, true
			}
		}
	}
	return slices.Insert(members, at, m)
}

// jsonInsert returns the object in data with key, which data must not have,
// set to value where order puts it (insertMember). The other members keep
// their order and values.
func jsonInsert(data []byte, key string, value []byte, order []string) ([]byte, error) {
	members, err := jsonMembers(data)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(members, func(m jsonMember) bool { return m.Key == key }) {
		return nil, fmt.Errorf("the object has %q already", key)
	}
	members = insertMember(members, jsonMember{Key: key, Value: value}, order)
	return formatJSONObject(members, len(bytes.TrimSpace(data)) == 0 || bytes.HasSuffix(data, []byte("\n")))
}
