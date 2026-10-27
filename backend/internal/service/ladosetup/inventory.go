package ladosetup

// The inventory (docs/LJY-PRD/HARNESS.md §9.1): one pass over the managed
// items, the tool folders and the projects. Every entry gets a status and,
// when LADO does not adopt it, a stable reason code. Adoptable and managed
// entries carry the size and sha256 of the content a version holds: a file's
// bytes, a folder's tree manifest (domain.LadoSetupTree) or a JSON value in
// canonical form, so a scan's sums are the ones adoption stores.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// setupTools are the tools LADO manages setup for.
var setupTools = []domain.LadoSetupTool{domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex}

// scanner collects the entries of one scan.
type scanner struct {
	deps    Deps
	entries []domain.LadoSetupEntry
	roots   []Root
	// linked are the locations whose root is itself a symbolic link.
	linked map[domain.LadoSetupLocation]bool
}

func (s *Service) scan(ctx context.Context) (Inventory, error) {
	var items []domain.LadoSetupItemWithVersion
	var adoptions []domain.LadoSetupAdoption
	if s.deps.Store != nil {
		var err error
		if items, err = s.deps.Store.ListLiveLadoSetupItems(ctx); err != nil {
			return Inventory{}, fmt.Errorf("list the managed setup items: %w", err)
		}
		if adoptions, err = s.deps.Store.ListLadoSetupAdoptions(ctx); err != nil {
			return Inventory{}, fmt.Errorf("list the setup adoptions: %w", err)
		}
	}
	sc := &scanner{deps: s.deps, linked: linkedRoots(s.deps)}
	sc.managed(items)
	sc.claudeDir(ctx)
	sc.claudeJSON()
	sc.codexHome(ctx)
	projects, err := sc.projects(ctx)
	if err != nil {
		return Inventory{}, err
	}
	drift := append(s.editedDrift(ctx, items), newLocalDrift(sc.entries, s.baselineSums())...)
	// A scan cut short reads folders as unreadable; it is not kept.
	if err := ctx.Err(); err != nil {
		return Inventory{}, fmt.Errorf("scan the local setup: %w", err)
	}
	verified := make(map[domain.LadoSetupTool]bool, len(setupTools))
	for _, tool := range setupTools {
		verified[tool] = domain.LadoSetupDeliveryVerified(s.deps.GOOS, tool)
	}
	return Inventory{
		Entries:   sc.entries,
		Roots:     sc.roots,
		Projects:  projects,
		Adoptions: adoptions,
		Drift:     drift,
		OS:        s.deps.GOOS,
		Verified:  verified,
		ScannedAt: s.deps.Clock().UTC(),
	}, nil
}

// managed lists the live items with their latest versions. An entry at an
// original location with the same name stays adoptable; drift tells the two
// apart.
func (sc *scanner) managed(items []domain.LadoSetupItemWithVersion) {
	for _, iv := range items {
		sc.entries = append(sc.entries, domain.LadoSetupEntry{
			Tool:     iv.Item.Tool,
			Kind:     iv.Item.Kind,
			Name:     iv.Item.Name,
			Location: domain.LadoSetupLocationManaged,
			Path:     managedPath(sc.deps.StateDir, iv.Item.Tool, iv.Item.Kind, iv.Item.Name),
			Size:     iv.Version.Size,
			SHA256:   iv.Version.SHA256,
			Status:   domain.LadoSetupStatusManaged,
			ItemID:   iv.Item.ID,
		})
	}
}

// managedPath is where LADO's managed folder keeps an item
// (docs/LJY-PRD/HARNESS.md §9.3), with "#<key>" after the file for a key. It
// is empty without a state dir.
func managedPath(stateDir string, tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string) string {
	if stateDir == "" {
		return ""
	}
	switch kind {
	case domain.LadoSetupKindSetting:
		return managedKeyFile(stateDir, tool, kind) + "#" + name
	case domain.LadoSetupKindMCPServer:
		if tool == domain.LadoSetupToolCodex {
			return managedKeyFile(stateDir, tool, kind) + "#mcp_servers." + name
		}
		return managedKeyFile(stateDir, tool, kind) + "#mcpServers." + name
	case domain.LadoSetupKindInstructions:
		return filepath.Join(managedRoot(stateDir, tool), filepath.FromSlash(name))
	}
	for _, d := range claudeItemDirs {
		if d.kind == kind {
			return filepath.Join(managedRoot(stateDir, tool), d.name, filepath.FromSlash(name))
		}
	}
	return ""
}

// add appends an entry LADO does not adopt, with its status and reason.
func (sc *scanner) add(e domain.LadoSetupEntry, status domain.LadoSetupStatus, reason domain.LadoSetupReason) {
	e.Status, e.Reason = status, reason
	sc.entries = append(sc.entries, e)
}

// linkedRoots are the locations whose root, the Claude folder,
// ~/.claude.json or the Codex folder, is a symbolic link, as into a dotfiles
// repository: adopting there would remove what the link points to. A
// ~/.claude.json inside a linked Claude folder lies behind the link as well.
// Only the root itself counts, not the folders above it.
func linkedRoots(deps Deps) map[domain.LadoSetupLocation]bool {
	isLink := func(p string) bool {
		info, err := os.Lstat(p)
		return p != "" && err == nil && info.Mode()&fs.ModeSymlink != 0
	}
	linked := map[domain.LadoSetupLocation]bool{
		domain.LadoSetupLocationClaudeDir:  isLink(deps.ClaudeDir),
		domain.LadoSetupLocationClaudeJSON: isLink(deps.ClaudeJSON),
		domain.LadoSetupLocationCodexHome:  isLink(deps.CodexHome),
	}
	if linked[domain.LadoSetupLocationClaudeDir] && deps.ClaudeJSON != "" && filepath.Dir(filepath.Clean(deps.ClaudeJSON)) == filepath.Clean(deps.ClaudeDir) {
		linked[domain.LadoSetupLocationClaudeJSON] = true
	}
	return linked
}

// adoptable appends an entry LADO can adopt, with its content's size and sum
// and what the adoption preview proposes for it. Under a linked root it is
// not adoptable.
func (sc *scanner) adoptable(e domain.LadoSetupEntry, size int64, sum string) {
	if sc.linked[e.Location] {
		sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonRootIsLink)
		return
	}
	e.Status, e.Size, e.SHA256 = domain.LadoSetupStatusAdoptable, size, sum
	e.DefaultAction = domain.LadoSetupDefaultAction(e.Tool, e.Kind, e.Name)
	sc.entries = append(sc.entries, e)
}

// item appends the entry for a file or folder item at e.Path, whose Lstat
// mode is mode: adoptable with its content, or why it is not.
func (sc *scanner) item(ctx context.Context, e domain.LadoSetupEntry, mode fs.FileMode) {
	var (
		size int64
		sum  string
		err  error
	)
	switch {
	case mode&fs.ModeSymlink != 0:
		sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
		return
	case mode.IsDir():
		var tree domain.LadoSetupTree
		if tree, err = treeOf(ctx, e.Path); err == nil {
			size = tree.Size()
			sum, err = tree.SHA256()
		}
	case mode.IsRegular():
		size, sum, err = fileSum(e.Path)
	default:
		err = fmt.Errorf("%s is not a regular file or a folder", e.Path)
	}
	switch {
	case errors.Is(err, errLink):
		sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	case err != nil:
		sc.unreadable(e, err)
	default:
		sc.adoptable(e, size, sum)
	}
}

// jsonItem appends an entry whose content is a JSON value.
func (sc *scanner) jsonItem(e domain.LadoSetupEntry, value []byte) {
	data, err := canonicalJSON(value)
	if err != nil {
		sc.unreadable(e, err)
		return
	}
	sc.adoptable(e, int64(len(data)), sha256Hex(data))
}

// unreadable appends an entry LADO could not read and logs why.
func (sc *scanner) unreadable(e domain.LadoSetupEntry, err error) {
	sc.deps.Log.Warn("LADO setup scan could not read an entry", "path", e.Path, "err", err)
	sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
}

// errLink marks a symbolic link inside a folder item.
var errLink = errors.New("is a symbolic link")

// treeOf builds the manifest of the folder at dir. It does not follow links:
// a link inside fails with errLink, and any other file that is not regular
// fails too.
func treeOf(ctx context.Context, dir string) (domain.LadoSetupTree, error) {
	var tree domain.LadoSetupTree
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s %w", path, errLink)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size, sum, err := fileSum(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		mode := domain.LadoSetupModeFile
		if info.Mode().Perm()&0o111 != 0 {
			mode = domain.LadoSetupModeExec
		}
		tree.Entries = append(tree.Entries, domain.LadoSetupTreeEntry{Path: filepath.ToSlash(rel), Mode: mode, Size: size, SHA256: sum})
		return nil
	})
	return tree, err
}

// fileSum returns the size and hex sha256 of the regular file at path, which
// must not be a link.
func fileSum(path string) (int64, string, error) {
	f, err := openRegular(path, false)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// readRegular reads the regular file at path, through a link.
func readRegular(path string) ([]byte, error) {
	f, err := openRegular(path, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// openRegular opens path for reading only when it is a regular file. It
// opens without blocking, so a named pipe fails instead of waiting for a
// writer forever, and checks the file it opened, not an earlier look at the
// path. Unless follow is set, a path that was a link when it was opened fails
// with errLink.
func openRegular(path string, follow bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	err = checkRegular(f, path, follow)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func checkRegular(f *os.File, path string, follow bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if follow {
		return nil
	}
	// The path is still the file that was opened, and not a link to it.
	if here, err := os.Lstat(path); err != nil || !os.SameFile(info, here) {
		return fmt.Errorf("%s %w", path, errLink)
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalJSON is the stored form of a JSON value: compact, object keys
// sorted, numbers as written and HTML characters left as they are. Its
// sha256 names the value's blob.
func canonicalJSON(value []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one JSON value")
	}
	return marshalCanonical(v)
}

// marshalCanonical encodes a decoded value as canonicalJSON does. It fails
// on what JSON cannot hold, such as a NaN.
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// jsonObject reads a JSON object's members. Empty input and null are an
// object without members.
func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, err
	}
	if members == nil {
		members = map[string]json.RawMessage{}
	}
	return members, nil
}

// sortedKeys returns a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// isMarkdown reports whether a file name ends in .md, in any case.
func isMarkdown(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md")
}
