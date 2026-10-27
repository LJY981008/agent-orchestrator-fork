package ladosetup

// Rescue of what sessions made in their config homes (plan §9, decision b).
// A session's Claude Code writes in its home as it would in its own folder:
// an agent it made with /agents lands in a merged folder, a server it added
// with `claude mcp add -s user` in the home's .claude.json, a memory edit in
// the home's copy of CLAUDE.md. Before LADO takes such an entry away, when it
// removes the home of an ended session or a reconcile replaces the entry, it
// rescues it:
//
//   - a regular file or folder LADO did not place goes to the same place in
//     the base, or beside what is there as <name>.lado-restored;
//   - a server the base and the managed items do not have goes into the
//     base's .claude.json when the base has none of that name;
//   - a managed copy the session changed replaces the managed folder's copy
//     when that still holds the latest version, so drift shows it for keep or
//     revert, or else goes beside it as <name>.lado-session-<session id>.
//
// Rescue follows no link and writes over nothing, and what it copies must
// hash as the original before the original goes. A home whose rescue fails
// stays for the next cleanup.

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sessionCopySuffix names a managed copy a session changed that LADO puts
// beside the managed folder's copy.
const sessionCopySuffix = ".lado-session-"

// homeRescue is a home to rescue from: its session, the record of what LADO
// placed in it and the base folder, empty when the record names none.
type homeRescue struct {
	session domain.SessionID
	home    string
	base    string
	record  *homeRecord
}

// rescueHome rescues what the session of the home folder dir made: the
// entries under the home and the servers of its .claude.json. A record
// without its base, written before LADO kept it, leaves the session's files
// and servers where they are; changed managed copies are rescued anyway.
func (s *Service) rescueHome(ctx context.Context, id domain.SessionID, dir string) error {
	record := readHomeRecord(filepath.Join(dir, homeRecordName))
	r := &homeRescue{session: id, home: filepath.Join(dir, "claude"), base: record.Base, record: record}
	if info, err := os.Lstat(r.home); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() {
		return cmp.Or(err, errors.New("the home is no folder"))
	}
	err := s.rescueEntries(ctx, r, "")
	if record.BaseConfig != "" {
		err = errors.Join(err, s.rescueServers(r.home, record, record.BaseConfig))
	}
	return err
}

// rescueEntries rescues what the session made in the home folder at key, a
// slash path under the home, "" for the home itself. Every entry the record
// does not name is the session's, but at the home's top level only Markdown
// and the item folders are rescued: the rest there is the home's own, as
// backups/ is. A managed copy is rescued when it changed, a link LADO placed
// that the session replaced with a file or folder like any entry of the
// session's, and a folder LADO placed is gone through.
func (s *Service) rescueEntries(ctx context.Context, r *homeRescue, key string) error {
	dir := filepath.Join(r.home, filepath.FromSlash(key))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range entries {
		child := path.Join(key, d.Name())
		p := filepath.Join(dir, d.Name())
		link := d.Type()&fs.ModeSymlink != 0
		switch kind := r.record.Entries[child]; {
		case link || child == claudeSettings:
			// A link loses nothing, and settings.json is LADO's.
		case kind == placedFile:
			if c, ok := r.record.Copies[child]; ok && d.Type().IsRegular() {
				errs = append(errs, s.rescueCopy(ctx, r, p, c))
			}
		case kind == placedFolder && d.IsDir():
			errs = append(errs, s.rescueEntries(ctx, r, child))
		case key == "" && kind == "" && !isMarkdown(d.Name()) && !isItemFolder(d.Name()):
			// The home's own.
		case d.IsDir() || d.Type().IsRegular():
			errs = append(errs, s.rescueToBase(ctx, r, child, p))
		}
	}
	return errors.Join(errs...)
}

func isItemFolder(name string) bool {
	for _, folder := range claudeItemDirs {
		if folder.name == name {
			return true
		}
	}
	return false
}

// rescueReplaced rescues what the session left at the home entry key, at p,
// which a reconcile is about to replace or take away: a managed copy it
// changed, what it made inside a folder LADO placed, or the file or folder it
// put where LADO placed something else. settings.json is LADO's, a link
// loses nothing, and an empty folder holds nothing.
func (s *Service) rescueReplaced(ctx context.Context, r *homeRescue, key, p string, info fs.FileInfo) error {
	kind := r.record.Entries[key]
	switch {
	case key == claudeSettings || info.Mode()&fs.ModeSymlink != 0 || (info.IsDir() && emptyFolder(p)):
		return nil
	case kind == placedFile && info.Mode().IsRegular():
		if c, ok := r.record.Copies[key]; ok {
			return s.rescueCopy(ctx, r, p, c)
		}
		return nil
	case kind == placedFolder && info.IsDir():
		return s.rescueEntries(ctx, r, key)
	case info.IsDir() || info.Mode().IsRegular():
		return s.rescueToBase(ctx, r, key, p)
	}
	return nil
}

// rescueToBase moves the session's file or folder at p, the home entry key,
// to the same place under the base. Without a base it stays.
func (s *Service) rescueToBase(ctx context.Context, r *homeRescue, key, p string) error {
	if r.base == "" {
		s.deps.Log.Warn("LADO leaves an entry a session made in its config home: the home names no base", "entry", key)
		return nil
	}
	if err := s.rescueTo(ctx, p, filepath.Join(r.base, filepath.FromSlash(key))); err != nil {
		return fmt.Errorf("rescue %s to the base: %w", key, err)
	}
	return nil
}

// rescueTo moves the regular file or folder at src to dst, or beside dst as
// <name>.lado-restored when dst is taken. A folder whose dst is a folder is
// merged into it entry by entry. It never follows a link and never writes
// over anything; the moved entry must hash as the original, and src goes
// last.
func (s *Service) rescueTo(ctx context.Context, src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	at, err := os.Lstat(dst)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case info.IsDir() && at.IsDir():
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		var errs []error
		for _, d := range entries {
			if d.IsDir() || d.Type().IsRegular() {
				errs = append(errs, s.rescueTo(ctx, filepath.Join(src, d.Name()), filepath.Join(dst, d.Name())))
			}
		}
		return errors.Join(errs...)
	default:
		if dst, err = freeBeside(dst, besidePath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), privateDir); err != nil {
		return err
	}
	if info.IsDir() {
		return s.moveFolder(ctx, src, dst)
	}
	return s.moveFile(src, dst, info)
}

// freeBeside is the first free path beside p that name gives for 1, 2 and
// on.
func freeBeside(p string, name func(string, int) string) (string, error) {
	for n := 1; ; n++ {
		candidate := name(p, n)
		_, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

// moveFile moves the regular file at src to dst, which is free: a hard link
// where the file system allows one, else a checked copy.
func (s *Service) moveFile(src, dst string, info fs.FileInfo) error {
	_, sum, err := fileSum(src)
	if err != nil {
		return err
	}
	if err := os.Link(src, dst); err != nil {
		staged := s.stagedPath(dst)
		if _, err := copyFile(src, staged); err != nil {
			_ = os.Remove(staged)
			return err
		}
		if err := os.Chmod(staged, info.Mode().Perm()); err != nil {
			_ = os.Remove(staged)
			return err
		}
		if err := placeStaged(staged, dst); err != nil {
			return err
		}
	}
	if _, got, err := fileSum(dst); err != nil || got != sum {
		return cmp.Or(err, errRestoredDiffers)
	}
	return os.Remove(src)
}

// moveFolder moves the folder at src to dst, which is free: a rename where
// the file system allows one, links inside included, else a checked copy of
// its files.
func (s *Service) moveFolder(ctx context.Context, src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	staged := s.stagedPath(dst)
	files, tree, err := copyTree(ctx, src, staged)
	if err == nil {
		err = chmodFiles(staged, files)
	}
	if err == nil {
		var want, got string
		if want, err = tree.SHA256(); err == nil {
			got, err = treeSum(ctx, staged)
		}
		if err == nil && got != want {
			err = errRestoredDiffers
		}
	}
	if err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	if err := placeStaged(staged, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// chmodFiles gives the copied files and folders under dir their modes.
func chmodFiles(dir string, files []backupFile) error {
	for _, f := range files {
		mode, err := parseMode(f.Mode)
		if err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(f.Path)), mode); err != nil {
			return err
		}
	}
	return nil
}

func treeSum(ctx context.Context, dir string) (string, error) {
	tree, err := treeOf(ctx, dir)
	if err != nil {
		return "", err
	}
	return tree.SHA256()
}

// rescueCopy rescues a managed copy at p in a home that the session changed
// from the version LADO placed: it becomes the managed folder's copy when
// that holds the latest version still, so drift shows it, or else goes
// beside it as <name>.lado-session-<session id>. An unchanged copy, or one
// the managed folder holds already, needs nothing.
func (s *Service) rescueCopy(ctx context.Context, r *homeRescue, p string, c homeCopy) error {
	data, err := readRegularNoLink(p)
	if err != nil {
		return err
	}
	sum := sha256Hex(data)
	if sum == c.SHA256 {
		return nil
	}
	it, versions, ok, err := s.deps.Store.LadoSetupItem(ctx, c.Item)
	if err != nil {
		return err
	}
	if !ok || len(versions) == 0 {
		return fmt.Errorf("LADO has no item %s for a managed copy", c.Item)
	}
	managed := managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name)
	if managed == "" {
		return fmt.Errorf("a %s item has no managed copy", it.Kind)
	}
	latest := versions[len(versions)-1]
	cur, err := readRegularNoLink(managed)
	switch {
	case err == nil && bytes.Equal(cur, data):
		return nil
	case err == nil && it.Live() && sha256Hex(cur) == latest.SHA256:
		info, err := os.Lstat(managed)
		if err != nil {
			return err
		}
		if err := replaceFile(managed, cur, data, info.Mode().Perm()); err != nil {
			return err
		}
		if now, err := readRegularNoLink(managed); err != nil || !bytes.Equal(now, data) {
			return cmp.Or(err, errRestoredDiffers)
		}
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return s.writeSessionCopy(managed, r.session, data)
}

// writeSessionCopy writes a session's changed copy beside the managed copy
// at managed, unless a copy beside it holds the same bytes already.
func (s *Service) writeSessionCopy(managed string, id domain.SessionID, data []byte) error {
	name := func(p string, n int) string {
		if n == 1 {
			return p + sessionCopySuffix + string(id)
		}
		return p + sessionCopySuffix + string(id) + "-" + strconv.Itoa(n)
	}
	for n := 1; ; n++ {
		p := name(managed, n)
		if was, err := readRegularNoLink(p); err == nil && bytes.Equal(was, data) {
			return nil
		}
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return s.writeNewFile(p, data, privateFile)
		}
	}
}

// rescueServers adds to the base's .claude.json at baseConfig the MCP
// servers the session added to the home's copy: those LADO did not put
// there. A server the base has by that name stays as it is. The base file is
// rewritten in place of itself, never through a link, with its mode.
func (s *Service) rescueServers(home string, record *homeRecord, baseConfig string) error {
	data, err := readRegularNoLink(filepath.Join(home, claudeConfigName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	added, err := sessionServers(data, record)
	if err != nil || len(added) == 0 {
		return err
	}
	return addServers(baseConfig, added)
}

// sessionServers are the MCP servers of a home's .claude.json that the
// record does not name.
func sessionServers(data []byte, record *homeRecord) ([]jsonMember, error) {
	value, ok, err := jsonMemberValue(data, claudeMCPKey)
	if err != nil || !ok {
		return nil, err
	}
	servers, err := jsonMembers(value)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(servers, func(m jsonMember) bool { return slices.Contains(record.Servers, m.Key) }), nil
}

// addServers puts servers into the .claude.json at p where it has none of
// their names, keeping the rest of the file in its order.
func addServers(p string, servers []jsonMember) error {
	info, err := os.Lstat(p)
	mode := privateFile
	var data []byte
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return errors.New("the base .claude.json is no regular file")
	default:
		mode = info.Mode().Perm()
		if data, err = readRegularNoLink(p); err != nil {
			return err
		}
	}
	out := data
	for _, m := range servers {
		if has, err := hasServer(out, m.Key); err != nil || has {
			if err != nil {
				return err
			}
			continue
		}
		if out, err = jsonWithMCPServer(out, m.Key, m.Value); err != nil {
			return err
		}
	}
	if bytes.Equal(out, data) {
		return nil
	}
	if data == nil {
		return writeNew(p, out, mode)
	}
	return replaceFile(p, data, out, mode)
}

func hasServer(data []byte, name string) (bool, error) {
	servers, ok, err := jsonMemberValue(data, claudeMCPKey)
	if err != nil || !ok {
		return false, err
	}
	_, has, err := jsonMemberValue(servers, name)
	return has, err
}
