package ladosetup

import (
	"testing"
)

// TestJSONEditKeepsOrder pins the order-keeping JSON edits adoption and
// restore make to settings.json and ~/.claude.json: the other members keep
// their order and values, values keep their numbers and escapes as written
// and are indented by two spaces, and the final newline stays as it was.
func TestJSONEditKeepsOrder(t *testing.T) {
	const settings = "{\n  \"env\": {\n    \"A\": \"1\"\n  },\n  \"model\": \"opus\",\n  \"hooks\": {},\n  \"theme\": \"dark\"\n}\n"
	cases := []struct {
		name string
		edit func() ([]byte, error)
		want string
	}{
		{
			name: "remove a middle key",
			edit: func() ([]byte, error) { return jsonWithout([]byte(settings), "model") },
			want: "{\n  \"env\": {\n    \"A\": \"1\"\n  },\n  \"hooks\": {},\n  \"theme\": \"dark\"\n}\n",
		},
		{
			name: "remove the first and the last key",
			edit: func() ([]byte, error) { return jsonWithout([]byte(settings), "env", "theme") },
			want: "{\n  \"model\": \"opus\",\n  \"hooks\": {}\n}\n",
		},
		{
			name: "remove every key",
			edit: func() ([]byte, error) { return jsonWithout([]byte(settings), "env", "model", "hooks", "theme") },
			want: "{}\n",
		},
		{
			name: "a missing key changes nothing",
			edit: func() ([]byte, error) { return jsonWithout([]byte(settings), "statusLine") },
			want: settings,
		},
		{
			name: "no final newline stays without one",
			edit: func() ([]byte, error) { return jsonWithout([]byte(`{"a": 1, "b": 2}`), "a") },
			want: "{\n  \"b\": 2\n}",
		},
		{
			name: "compact values are indented and keep numbers and escapes",
			edit: func() ([]byte, error) {
				return jsonWithout([]byte(`{"x":1,"p":{"allow":["Bash(ls)"],"n":1.50,"e":"\u00e9<"},"q":[]}`+"\n"), "x")
			},
			want: "{\n  \"p\": {\n    \"allow\": [\n      \"Bash(ls)\"\n    ],\n    \"n\": 1.50,\n    \"e\": \"\\u00e9<\"\n  },\n  \"q\": []\n}\n",
		},
		{
			name: "set a key in its place",
			edit: func() ([]byte, error) { return jsonWith([]byte(settings), "model", []byte(`"sonnet"`)) },
			want: "{\n  \"env\": {\n    \"A\": \"1\"\n  },\n  \"model\": \"sonnet\",\n  \"hooks\": {},\n  \"theme\": \"dark\"\n}\n",
		},
		{
			name: "set a new key last",
			edit: func() ([]byte, error) {
				return jsonWith([]byte(settings), "statusLine", []byte(`{"type":"command","command":"x"}`))
			},
			want: "{\n  \"env\": {\n    \"A\": \"1\"\n  },\n  \"model\": \"opus\",\n  \"hooks\": {},\n  \"theme\": \"dark\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"x\"\n  }\n}\n",
		},
		{
			name: "set a key in an empty file",
			edit: func() ([]byte, error) { return jsonWith(nil, "model", []byte(`"opus"`)) },
			want: "{\n  \"model\": \"opus\"\n}\n",
		},
		{
			name: "remove a member of a nested object",
			edit: func() ([]byte, error) {
				return jsonWithoutMCPServer([]byte("{\n  \"n\": 3,\n  \"mcpServers\": {\n    \"a\": {\n      \"command\": \"a\"\n    },\n    \"b\": {\n      \"url\": \"https://b\"\n    }\n  },\n  \"projects\": {}\n}\n"), "a")
			},
			want: "{\n  \"n\": 3,\n  \"mcpServers\": {\n    \"b\": {\n      \"url\": \"https://b\"\n    }\n  },\n  \"projects\": {}\n}\n",
		},
		{
			name: "the last nested member leaves an empty object",
			edit: func() ([]byte, error) { return jsonWithoutMCPServer([]byte(`{"mcpServers":{"a":{}}}`), "a") },
			want: "{\n  \"mcpServers\": {}\n}",
		},
		{
			name: "set a nested member in a new object",
			edit: func() ([]byte, error) { return jsonWithMCPServer(nil, "a", []byte(`{"command":"a"}`)) },
			want: "{\n  \"mcpServers\": {\n    \"a\": {\n      \"command\": \"a\"\n    }\n  }\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.edit()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}

	for _, bad := range []string{`[1, 2]`, `"text"`, `{"a": 1`, `{"a": 1} {"b": 2}`, `{"a": nope}`} {
		if out, err := jsonWithout([]byte(bad), "a"); err == nil {
			t.Fatalf("jsonWithout(%s) = %s, want an error", bad, out)
		}
	}
	if _, err := jsonWith([]byte(`{"a": 1}`), "b", []byte(`{bad`)); err == nil {
		t.Fatal("jsonWith takes a value that is no JSON")
	}
	if _, err := jsonWithoutMCPServer([]byte(`{"a": 1}`), "x"); err == nil {
		t.Fatal("jsonWithoutMCPServer takes a file without mcpServers")
	}
}
