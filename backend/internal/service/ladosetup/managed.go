package ladosetup

// LADO's managed folder (docs/LJY-PRD/HARNESS.md §9.3): <state>/lado/harness/
// claude/ and codex/ hold the live items as files a person can read. Every
// write comes from a stored version (a blob), never from what the folder held
// before, so the folder always shows versions LADO recorded: files and
// folders at their managed paths (managedPath), settings keys in
// settings.json, Claude's MCP servers in mcp.json as {"mcpServers": {...}},
// and Codex's in config.toml as [mcp_servers.<name>] tables. A key file
// changes only in the key written, so an edit made there by hand to another
// key stays for drift to show.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxBlobBytes bounds one blob LADO reads back for the managed folder.
const maxBlobBytes = 256 << 20

// Modes of the managed folder and the backups: owner only, since MCP servers
// can hold secrets.
const (
	privateDir  fs.FileMode = 0o700
	privateFile fs.FileMode = 0o600
	privateExec fs.FileMode = 0o700
)

// managedKeyFile is the managed file that holds the items of a key kind:
// Claude's settings.json and mcp.json, and Codex's config.toml. It is empty
// for the other kinds and without a state dir.
func managedKeyFile(stateDir string, tool domain.LadoSetupTool, kind domain.LadoSetupKind) string {
	if stateDir == "" {
		return ""
	}
	root := managedRoot(stateDir, tool)
	switch {
	case tool == domain.LadoSetupToolCodex && kind == domain.LadoSetupKindMCPServer:
		return filepath.Join(root, codexConfig)
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindMCPServer:
		return filepath.Join(root, "mcp.json")
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindSetting:
		return filepath.Join(root, claudeSettings)
	}
	return ""
}

// managedRoot is a tool's part of the managed folder.
func managedRoot(stateDir string, tool domain.LadoSetupTool) string {
	if tool == domain.LadoSetupToolCodex {
		return filepath.Join(stateDir, "lado", "harness", "codex")
	}
	return filepath.Join(stateDir, "lado", "harness", "claude")
}

// keyValue reads the content of a key item from the bytes of the file that
// holds it, an original or a managed one: a settings value (env without its
// login variables), a Claude MCP server from mcpServers, or a Codex MCP
// server table. The content is canonical JSON, as versions store it.
func keyValue(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string, file []byte) ([]byte, error) {
	switch {
	case tool == domain.LadoSetupToolCodex && kind == domain.LadoSetupKindMCPServer:
		var doc map[string]any
		if err := toml.Unmarshal(file, &doc); err != nil {
			return nil, err
		}
		servers, _ := doc[codexServersKey].(map[string]any)
		table, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no [%s.%s] table", codexServersKey, name)
		}
		return marshalCanonical(table)
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindMCPServer:
		servers, ok, err := jsonMemberValue(file, claudeMCPKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("no %s", claudeMCPKey)
		}
		value, ok, err := jsonMemberValue(servers, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("no MCP server %s", name)
		}
		return canonicalJSON(value)
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindSetting:
		value, ok, err := jsonMemberValue(file, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("no setting %s", name)
		}
		if name == claudeEnvKey {
			if value, _, err = withoutLoginEnv(value); err != nil {
				return nil, err
			}
			if value == nil {
				return nil, errors.New("env holds login variables only")
			}
		}
		return canonicalJSON(value)
	}
	return nil, fmt.Errorf("%s %s is not a key item", tool, kind)
}

// writeManaged writes one item version into the managed folder from its
// blobs.
func (s *Service) writeManaged(iv domain.LadoSetupItemWithVersion) error {
	it, v := iv.Item, iv.Version
	if s.deps.Blobs == nil || s.deps.StateDir == "" {
		return errors.New("LADO setup has no blob store or state dir")
	}
	switch v.ContentKind {
	case domain.LadoSetupContentFile:
		data, err := s.deps.Blobs.Get(v.SHA256, maxBlobBytes)
		if err != nil {
			return fmt.Errorf("read %s from its blob: %w", it.Name, err)
		}
		mode := privateFile
		if v.Executable {
			mode = privateExec
		}
		path := managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name)
		if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
			return err
		}
		// A folder left at the path by an item that ended gives way.
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		return atomicWrite(path, data, mode, os.Rename)
	case domain.LadoSetupContentTree:
		return s.writeManagedTree(managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name), v)
	case domain.LadoSetupContentJSON:
		content, err := s.deps.Blobs.Get(v.SHA256, maxBlobBytes)
		if err != nil {
			return fmt.Errorf("read %s from its blob: %w", it.Name, err)
		}
		return s.editManagedKeys(it, func(file []byte) ([]byte, error) { return withKey(it, file, content) })
	}
	return fmt.Errorf("unknown content kind %q", v.ContentKind)
}

// removeManaged takes an item out of the managed folder. An item that is not
// there is no error.
func (s *Service) removeManaged(it domain.LadoSetupItem) error {
	if s.deps.StateDir == "" {
		return errors.New("LADO setup has no state dir")
	}
	if it.Kind == domain.LadoSetupKindSetting || it.Kind == domain.LadoSetupKindMCPServer {
		return s.editManagedKeys(it, func(file []byte) ([]byte, error) { return withoutKey(it, file) })
	}
	return os.RemoveAll(managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name))
}

// editManagedKeys rewrites the managed key file of an item with edit. A file
// that does not exist yet is empty.
func (s *Service) editManagedKeys(it domain.LadoSetupItem, edit func([]byte) ([]byte, error)) error {
	path := managedKeyFile(s.deps.StateDir, it.Tool, it.Kind)
	if path == "" {
		return fmt.Errorf("%s %s has no managed key file", it.Tool, it.Kind)
	}
	data, err := readRegularNoLink(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := edit(data)
	if err != nil {
		return fmt.Errorf("edit %s: %w", filepath.Base(path), err)
	}
	if bytes.Equal(out, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
		return err
	}
	return atomicWrite(path, out, privateFile, os.Rename)
}

// withKey sets a key item in the bytes of the file that holds it.
func withKey(it domain.LadoSetupItem, file, content []byte) ([]byte, error) {
	switch {
	case it.Tool == domain.LadoSetupToolCodex:
		block, err := codexServerBlock(it.Name, content)
		if err != nil {
			return nil, err
		}
		if _, err := keyValue(it.Tool, it.Kind, it.Name, file); err == nil {
			if file, _, err = tomlWithoutServer(file, it.Name); err != nil {
				return nil, err
			}
		}
		return tomlWithBlock(file, block)
	case it.Kind == domain.LadoSetupKindMCPServer:
		return jsonWithMCPServer(file, it.Name, content)
	default:
		return jsonWith(file, it.Name, content)
	}
}

// withoutKey takes a key item out of the bytes of the file that holds it. A
// file without the key comes back as it was.
func withoutKey(it domain.LadoSetupItem, file []byte) ([]byte, error) {
	if len(bytes.TrimSpace(file)) == 0 {
		return file, nil
	}
	if _, err := keyValue(it.Tool, it.Kind, it.Name, file); err != nil {
		return file, nil //nolint:nilerr // nothing to take out
	}
	switch {
	case it.Tool == domain.LadoSetupToolCodex:
		rest, _, err := tomlWithoutServer(file, it.Name)
		return rest, err
	case it.Kind == domain.LadoSetupKindMCPServer:
		return jsonWithoutMCPServer(file, it.Name)
	default:
		return jsonWithout(file, it.Name)
	}
}

// writeManagedTree writes a folder item into a new folder beside its path and
// swaps it in, so the managed path never holds half a folder.
func (s *Service) writeManagedTree(path string, v domain.LadoSetupItemVersion) error {
	tree, err := s.treeOfVersion(v)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, privateDir); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(path)+".lado-")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(tmp)
		}
	}()
	for _, e := range tree.Entries {
		data, err := s.deps.Blobs.Get(e.SHA256, maxBlobBytes)
		if err != nil {
			return fmt.Errorf("read %s from its blob: %w", e.Path, err)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(dst), privateDir); err != nil {
			return err
		}
		mode := privateFile
		if e.Mode == domain.LadoSetupModeExec {
			mode = privateExec
		}
		if err := writeNew(dst, data, mode); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	done = true
	return nil
}

// treeOfVersion reads a folder item version's manifest from its blob and
// checks it against the version's sha256.
func (s *Service) treeOfVersion(v domain.LadoSetupItemVersion) (domain.LadoSetupTree, error) {
	data, err := s.deps.Blobs.Get(v.SHA256, maxBlobBytes)
	if err != nil {
		return domain.LadoSetupTree{}, fmt.Errorf("read the folder manifest: %w", err)
	}
	var tree domain.LadoSetupTree
	if err := json.Unmarshal(data, &tree); err != nil {
		return domain.LadoSetupTree{}, fmt.Errorf("read the folder manifest: %w", err)
	}
	tree, err = tree.Normalize()
	if err != nil {
		return domain.LadoSetupTree{}, err
	}
	if sum, err := tree.SHA256(); err != nil || sum != v.SHA256 {
		return domain.LadoSetupTree{}, errors.New("the folder manifest does not hash to its version")
	}
	return tree, nil
}

// managedSum returns the sha256 of an item's content in the managed folder,
// as its versions name content: a file's bytes, a folder's tree or a key's
// canonical JSON.
func (s *Service) managedSum(ctx context.Context, it domain.LadoSetupItem) (string, error) {
	switch it.Kind {
	case domain.LadoSetupKindSetting, domain.LadoSetupKindMCPServer:
		data, err := readRegularNoLink(managedKeyFile(s.deps.StateDir, it.Tool, it.Kind))
		if err != nil {
			return "", err
		}
		content, err := keyValue(it.Tool, it.Kind, it.Name, data)
		if err != nil {
			return "", err
		}
		return sha256Hex(content), nil
	}
	path := managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		tree, err := treeOf(ctx, path)
		if err != nil {
			return "", err
		}
		return tree.SHA256()
	}
	_, sum, err := fileSum(path)
	return sum, err
}

// checkManaged reports how the managed copy of an item differs from its
// version, or nil when it holds the version.
func (s *Service) checkManaged(ctx context.Context, iv domain.LadoSetupItemWithVersion) error {
	sum, err := s.managedSum(ctx, iv.Item)
	if err != nil {
		return err
	}
	if sum != iv.Version.SHA256 {
		return fmt.Errorf("the managed copy hashes to %s, not %s", sum, iv.Version.SHA256)
	}
	if iv.Version.ContentKind == domain.LadoSetupContentFile && runtime.GOOS != "windows" {
		info, err := os.Lstat(managedPath(s.deps.StateDir, iv.Item.Tool, iv.Item.Kind, iv.Item.Name))
		if err != nil {
			return err
		}
		if exec := info.Mode().Perm()&0o111 != 0; exec != iv.Version.Executable {
			return fmt.Errorf("the managed copy is executable=%t, not %t", exec, iv.Version.Executable)
		}
	}
	return nil
}

// codexServerBlock is the [mcp_servers.<name>] table of an MCP server's
// content, one key a line in key order, each value inline TOML.
func codexServerBlock(name string, content []byte) ([]byte, error) {
	table, err := codexServerTable(name, content)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%s.%s]\n", codexServersKey, name)
	for _, key := range sortedKeys(table) {
		value, err := tomlInline(table[key])
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", name, key, err)
		}
		fmt.Fprintf(&buf, "%s = %s\n", tomlKeyText(key), value)
	}
	return buf.Bytes(), nil
}

// codexServerOverride is the -c value that gives a Codex session an MCP
// server: mcp_servers.<name>=<inline table> (plan decision d).
func codexServerOverride(name string, content []byte) (string, error) {
	table, err := codexServerTable(name, content)
	if err != nil {
		return "", err
	}
	value, err := tomlInline(table)
	if err != nil {
		return "", err
	}
	return codexServersKey + "." + name + "=" + value, nil
}

// checkCodexOverride parses a server's -c value back with the TOML parser
// and compares it with the content.
func checkCodexOverride(name string, content []byte) error {
	value, err := codexServerOverride(name, content)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(value), &doc); err != nil {
		return fmt.Errorf("the -c value does not parse: %w", err)
	}
	servers, _ := doc[codexServersKey].(map[string]any)
	got, err := marshalCanonical(servers[name])
	if err != nil {
		return err
	}
	want, err := canonicalJSON(content)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("the -c value reads back as %s, not %s", got, want)
	}
	return nil
}

func codexServerTable(name string, content []byte) (map[string]any, error) {
	if !codexServerName.MatchString(name) {
		return nil, fmt.Errorf("the MCP server name %q needs quoting", name)
	}
	v, err := decodeNumbered(content)
	if err != nil {
		return nil, err
	}
	table, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the MCP server %s is not a table", name)
	}
	return table, nil
}

// decodeNumbered decodes JSON with numbers kept as written.
func decodeNumbered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// tomlInline writes a decoded JSON value as an inline TOML value. JSON
// numbers are TOML numbers as written; null has no TOML form.
func tomlInline(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return tomlString(v), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return v.String(), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			s, err := tomlInline(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		parts := make([]string, 0, len(v))
		for _, key := range sortedKeys(v) {
			s, err := tomlInline(v[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKeyText(key)+" = "+s)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", fmt.Errorf("TOML has no form of %T", v)
}

var bareTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlKeyText writes a key bare when it can be, else as a basic string.
func tomlKeyText(key string) string {
	if bareTOMLKey.MatchString(key) {
		return key
	}
	return tomlString(key)
}

// tomlString writes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
