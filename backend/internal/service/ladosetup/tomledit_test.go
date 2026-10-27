package ladosetup

import (
	"strings"
	"testing"
)

// TestTOMLEditRemovesOnlyTheTable pins the line edits of config.toml: a
// server's table and sub-tables are cut, comments and other tables stay,
// the cut text puts the server back, and an edit that parsing shows to be
// wrong fails instead of writing.
func TestTOMLEditRemovesOnlyTheTable(t *testing.T) {
	const config = `# my codex config
model = "gpt-5"

[mcp_servers.figma]
url = "https://figma"

# notion is for docs
[mcp_servers.notion]
url = "https://notion"
args = ["--x"]

[projects."/x"]
trust_level = "trusted"
`
	cases := []struct {
		name, data, server string
		want, block        string
	}{
		{
			name: "a table between others; the comment above the next stays",
			data: config, server: "figma",
			want:  "# my codex config\nmodel = \"gpt-5\"\n\n# notion is for docs\n[mcp_servers.notion]\nurl = \"https://notion\"\nargs = [\"--x\"]\n\n[projects.\"/x\"]\ntrust_level = \"trusted\"\n",
			block: "[mcp_servers.figma]\nurl = \"https://figma\"\n\n",
		},
		{
			name: "a table with a comment above it keeps that comment",
			data: config, server: "notion",
			want:  "# my codex config\nmodel = \"gpt-5\"\n\n[mcp_servers.figma]\nurl = \"https://figma\"\n\n# notion is for docs\n[projects.\"/x\"]\ntrust_level = \"trusted\"\n",
			block: "[mcp_servers.notion]\nurl = \"https://notion\"\nargs = [\"--x\"]\n\n",
		},
		{
			name: "the last table takes the blank lines before it",
			data: "model = \"x\"\n\n[mcp_servers.a]\ncommand = \"a\"\n", server: "a",
			want:  "model = \"x\"\n",
			block: "[mcp_servers.a]\ncommand = \"a\"\n",
		},
		{
			name:   "sub-tables go wherever they are, quoted headers and comments too",
			data:   "[mcp_servers.a]\ncommand = \"a\" # run a\n\n[mcp_servers.b]\nurl = \"b\"\n\n[ mcp_servers . \"a\" . env_vars ] # names only\nX = \"X\"\n\n[[mcp_servers.a.tools]]\nname = \"t\"\n",
			server: "a",
			want:   "[mcp_servers.b]\nurl = \"b\"\n\n",
			block:  "[mcp_servers.a]\ncommand = \"a\" # run a\n\n[ mcp_servers . \"a\" . env_vars ] # names only\nX = \"X\"\n\n[[mcp_servers.a.tools]]\nname = \"t\"\n",
		},
		{
			name: "no final newline",
			data: "[mcp_servers.a]\ncommand = \"a\"\n\n[mcp_servers.b]\ncommand = \"b\"", server: "b",
			want:  "[mcp_servers.a]\ncommand = \"a\"\n",
			block: "[mcp_servers.b]\ncommand = \"b\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, block, err := tomlWithoutServer([]byte(tc.data), tc.server)
			if err != nil {
				t.Fatal(err)
			}
			if string(rest) != tc.want {
				t.Fatalf("rest\n%s\nwant\n%s", rest, tc.want)
			}
			if string(block) != tc.block {
				t.Fatalf("block\n%s\nwant\n%s", block, tc.block)
			}
			back, err := tomlWithBlock(rest, block)
			if err != nil {
				t.Fatalf("put the server back: %v", err)
			}
			want, err := keyValue("codex", "mcp_server", tc.server, []byte(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := keyValue("codex", "mcp_server", tc.server, back)
			if err != nil || string(got) != string(want) {
				t.Fatalf("the server put back reads %s (%v), want %s", got, err, want)
			}
		})
	}

	refused := []struct{ name, data, server, want string }{
		{"no such table", config, "slack", "no [mcp_servers.slack] table"},
		{"an inline server", "mcp_servers = { a = { command = \"a\" } }\n", "a", "no [mcp_servers.a] table"},
		{
			// A header look-alike inside a multi-line string fools the line
			// reader; parsing the result catches it.
			"a header inside a multi-line string",
			"[mcp_servers.a]\ncommand = \"a\"\n\n[x]\nnote = \"\"\"\n[mcp_servers.a.env]\n\"\"\"\n", "a", "",
		},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			rest, _, err := tomlWithoutServer([]byte(tc.data), tc.server)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tomlWithoutServer = %q, %v; want an error with %q", rest, err, tc.want)
			}
		})
	}

	if _, err := tomlWithBlock([]byte(config), []byte("[mcp_servers.figma]\nurl = \"other\"\n")); err == nil {
		t.Fatal("tomlWithBlock adds a server config.toml already has")
	}
	got, err := tomlWithBlock(nil, []byte("[mcp_servers.a]\ncommand = \"a\"\n"))
	if err != nil || string(got) != "[mcp_servers.a]\ncommand = \"a\"\n" {
		t.Fatalf("tomlWithBlock on an empty file = %q, %v", got, err)
	}
}
