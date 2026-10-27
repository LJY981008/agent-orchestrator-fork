package ladosetup

// The adoption backup (plan §7.3, docs/LJY-PRD/HARNESS.md §9.2 step 5).
// Before LADO removes anything, every original it moves is copied to
// <state>/lado/backups/<adoption-id>/files/<root>/<relative path>, a file it
// takes keys out of whole, with manifest.json beside the copies. The copies
// are owner-only (MCP servers can hold secrets) and keep their mtimes; the
// manifest keeps each original's mode and mtime, and its folder's files and
// subfolders. Everything is flushed, then read back and hashed: removal
// starts only from a backup that matches its manifest and what the preview
// hashed.

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
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const backupFormat = "lado-setup-backup/1"

// Backup entry types.
const (
	backupTypeFile = "file"
	backupTypeDir  = "dir"
	// backupTypeKeys is a file LADO takes keys out of, backed up whole.
	backupTypeKeys = "keys"
)

// backupManifest is manifest.json of an adoption's backup.
type backupManifest struct {
	Format     string                     `json:"format"`
	AdoptionID domain.LadoSetupAdoptionID `json:"adoptionId"`
	CreatedAt  time.Time                  `json:"createdAt"`
	OS         string                     `json:"os"`
	PlanSHA256 string                     `json:"planSha256"`
	// Roots are the original folders, and the global config file, that the
	// entries' relative paths start from.
	Roots   map[domain.LadoSetupLocation]string `json:"roots"`
	Entries []backupEntry                       `json:"entries"`
}

// backupEntry is the original of one adopted item.
type backupEntry struct {
	Tool   domain.LadoSetupTool   `json:"tool"`
	Kind   domain.LadoSetupKind   `json:"kind"`
	Name   string                 `json:"name"`
	Action domain.LadoSetupAction `json:"action"`
	// Root and RelPath place the original under its root, with slashes, and
	// its copy at files/<root>/<relPath>; Path is the original's absolute
	// path.
	Root    domain.LadoSetupLocation `json:"root"`
	RelPath string                   `json:"relPath"`
	Path    string                   `json:"path"`
	Type    string                   `json:"type"`
	// SHA256 and Size are the file's bytes, the folder's tree
	// (domain.LadoSetupTree) or the whole key file. Mode is the original's
	// permission bits in octal.
	SHA256 string    `json:"sha256"`
	Size   int64     `json:"size"`
	Mode   string    `json:"mode"`
	MTime  time.Time `json:"mtime"`
	// Files are a folder's files and subfolders, in walk order.
	Files []backupFile `json:"files,omitempty"`
	// RemovedKeys are what a keys entry takes out of its file.
	RemovedKeys []backupKey `json:"removedKeys,omitempty"`
}

// backupFile is a file or subfolder of a backed-up folder.
type backupFile struct {
	Path   string    `json:"path"`
	Dir    bool      `json:"dir,omitempty"`
	Mode   string    `json:"mode"`
	MTime  time.Time `json:"mtime"`
	Size   int64     `json:"size,omitempty"`
	SHA256 string    `json:"sha256,omitempty"`
}

// backupKey is a key adoption takes out of a file.
type backupKey struct {
	// Key is the top-level key; Member names the server inside mcpServers or
	// mcp_servers.
	Key    string `json:"key"`
	Member string `json:"member,omitempty"`
	// SHA256 is the sha256 of the value LADO adopts, as its version stores
	// it, before any hook path rewrite.
	SHA256 string `json:"sha256"`
	// KeptInPlace are the login variables an env setting leaves in the
	// file.
	KeptInPlace []string `json:"keptInPlace,omitempty"`
}

// verifiedBackup is a backup whose copies were read back and match both the
// manifest and the plan.
type verifiedBackup struct {
	plan     adoptionPlan
	dir      string
	manifest backupManifest
	// sha256 is the hash of manifest.json, which the adoption records.
	sha256 string
}

// backupDir is where an adoption's backup lives.
func backupDir(stateDir string, id domain.LadoSetupAdoptionID) string {
	return filepath.Join(stateDir, "lado", "backups", string(id))
}

// copyPath is where the backup keeps the copy of an entry's original.
func (e backupEntry) copyPath(dir string) string {
	return filepath.Join(dir, "files", string(e.Root), filepath.FromSlash(e.RelPath))
}

// backup copies the originals the plan moves and verifies the copies.
func (s *Service) backup(ctx context.Context, id domain.LadoSetupAdoptionID, plan adoptionPlan) (verifiedBackup, error) {
	s.stage(stageBackup)
	dir := backupDir(s.deps.StateDir, id)
	if err := os.MkdirAll(filepath.Join(dir, "files"), privateDir); err != nil {
		return verifiedBackup{}, &stageError{stage: stageBackup, err: err}
	}
	m := backupManifest{
		Format:     backupFormat,
		AdoptionID: id,
		CreatedAt:  s.now(),
		OS:         s.deps.GOOS,
		PlanSHA256: plan.preview.PlanSHA256,
		Roots:      s.backupRoots(),
	}
	// A key file is copied once for all the keys taken out of it.
	copied := map[string]fileFacts{}
	for _, pe := range plan.moves() {
		be, err := s.backupOne(ctx, dir, pe, copied)
		if err != nil {
			return verifiedBackup{}, &stageError{stage: stageBackup, root: be.Root, path: be.RelPath, err: err}
		}
		m.Entries = append(m.Entries, be)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return verifiedBackup{}, &stageError{stage: stageBackup, err: err}
	}
	data = append(data, '\n')
	if err := writeNew(filepath.Join(dir, "manifest.json"), data, privateFile); err != nil {
		return verifiedBackup{}, &stageError{stage: stageBackup, err: err}
	}
	if err := syncTree(dir); err != nil {
		return verifiedBackup{}, &stageError{stage: stageBackup, err: err}
	}
	if s.afterBackup != nil {
		s.afterBackup(dir)
	}
	if err := verifyBackup(ctx, dir, data, plan); err != nil {
		return verifiedBackup{}, err
	}
	s.stage(stageBackupVerified)
	return verifiedBackup{plan: plan, dir: dir, manifest: m, sha256: sha256Hex(data)}, nil
}

// backupRoots are the roots the daemon gave.
func (s *Service) backupRoots() map[domain.LadoSetupLocation]string {
	roots := map[domain.LadoSetupLocation]string{}
	for loc, path := range map[domain.LadoSetupLocation]string{
		domain.LadoSetupLocationClaudeDir:  s.deps.ClaudeDir,
		domain.LadoSetupLocationClaudeJSON: s.deps.ClaudeJSON,
		domain.LadoSetupLocationCodexHome:  s.deps.CodexHome,
	} {
		if path != "" {
			roots[loc] = path
		}
	}
	return roots
}

// origin returns where an entry's original is: its absolute path (the file
// that holds a key) and that path under its root, with slashes.
func (s *Service) origin(e domain.LadoSetupEntry) (abs, rel string) {
	abs = e.Path
	if isKeyKind(e.Kind) {
		abs = s.originalKeyFile(e.Tool, e.Kind)
	}
	switch e.Location {
	case domain.LadoSetupLocationClaudeJSON:
		return abs, filepath.Base(abs)
	case domain.LadoSetupLocationCodexHome:
		return abs, slashRel(s.deps.CodexHome, abs)
	default:
		return abs, slashRel(s.deps.ClaudeDir, abs)
	}
}

// originalKeyFile is the original file that holds the items of a key kind.
func (s *Service) originalKeyFile(tool domain.LadoSetupTool, kind domain.LadoSetupKind) string {
	switch {
	case tool == domain.LadoSetupToolCodex && kind == domain.LadoSetupKindMCPServer:
		return filepath.Join(s.deps.CodexHome, codexConfig)
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindMCPServer:
		return s.deps.ClaudeJSON
	case tool == domain.LadoSetupToolClaudeCode && kind == domain.LadoSetupKindSetting:
		return filepath.Join(s.deps.ClaudeDir, claudeSettings)
	}
	return ""
}

// backupOne copies one original into the backup and describes it.
func (s *Service) backupOne(ctx context.Context, dir string, pe planEntry, copied map[string]fileFacts) (backupEntry, error) {
	e := pe.entry
	abs, rel := s.origin(e)
	be := backupEntry{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Action: pe.action, Root: e.Location, RelPath: rel, Path: abs}
	dst := be.copyPath(dir)
	if err := os.MkdirAll(filepath.Dir(dst), privateDir); err != nil {
		return be, err
	}
	if isKeyKind(e.Kind) {
		facts, ok := copied[dst]
		if !ok {
			var err error
			if facts, err = copyFile(abs, dst); err != nil {
				return be, err
			}
			copied[dst] = facts
		}
		be.Type = backupTypeKeys
		be.SHA256, be.Size, be.Mode, be.MTime = facts.sha256, facts.size, modeText(facts.mode), facts.mtime
		be.RemovedKeys = []backupKey{removedKey(e)}
		return be, nil
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return be, err
	}
	be.Mode, be.MTime = modeText(info.Mode()), info.ModTime()
	switch {
	case info.IsDir():
		files, tree, err := copyTree(ctx, abs, dst)
		if err != nil {
			return be, err
		}
		sum, err := tree.SHA256()
		if err != nil {
			return be, err
		}
		be.Type, be.Files, be.SHA256, be.Size = backupTypeDir, files, sum, tree.Size()
		return be, os.Chtimes(dst, info.ModTime(), info.ModTime())
	case info.Mode().IsRegular():
		facts, err := copyFile(abs, dst)
		if err != nil {
			return be, err
		}
		be.Type, be.SHA256, be.Size = backupTypeFile, facts.sha256, facts.size
		return be, nil
	}
	return be, fmt.Errorf("%s is not a regular file or a folder", abs)
}

// removedKey is what adoption takes out of a key file for an entry.
func removedKey(e domain.LadoSetupEntry) backupKey {
	switch {
	case e.Kind == domain.LadoSetupKindMCPServer && e.Tool == domain.LadoSetupToolCodex:
		return backupKey{Key: codexServersKey, Member: e.Name, SHA256: e.SHA256}
	case e.Kind == domain.LadoSetupKindMCPServer:
		return backupKey{Key: claudeMCPKey, Member: e.Name, SHA256: e.SHA256}
	default:
		return backupKey{Key: e.Name, SHA256: e.SHA256, KeptInPlace: slices.Clone(e.KeptInPlace)}
	}
}

// fileFacts describe a file as it was read.
type fileFacts struct {
	sha256 string
	size   int64
	mode   fs.FileMode
	mtime  time.Time
}

// copyFile copies the regular file at src, which must not be a link, to a
// new owner-only file at dst with src's mtime, flushed. It returns what it
// read.
func copyFile(src, dst string) (fileFacts, error) {
	in, err := openRegular(src, false)
	if err != nil {
		return fileFacts{}, err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return fileFacts{}, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFile)
	if err != nil {
		return fileFacts{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fileFacts{}, err
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return fileFacts{}, err
	}
	return fileFacts{sha256: hex.EncodeToString(h.Sum(nil)), size: n, mode: info.Mode(), mtime: info.ModTime()}, nil
}

// copyTree copies the folder at src to dst, which it creates. A link or a
// special file inside fails. It returns the folder's files and subfolders
// and its tree.
func copyTree(ctx context.Context, src, dst string) ([]backupFile, domain.LadoSetupTree, error) {
	var (
		files []backupFile
		tree  domain.LadoSetupTree
		dirs  []backupFile
	)
	if err := os.Mkdir(dst, privateDir); err != nil {
		return nil, tree, err
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s %w", path, errLink)
		case d.IsDir():
			f := backupFile{Path: filepath.ToSlash(rel), Dir: true, Mode: modeText(info.Mode()), MTime: info.ModTime()}
			files = append(files, f)
			dirs = append(dirs, f)
			return os.Mkdir(target, privateDir)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", path)
		}
		facts, err := copyFile(path, target)
		if err != nil {
			return err
		}
		files = append(files, backupFile{Path: filepath.ToSlash(rel), Mode: modeText(facts.mode), MTime: facts.mtime, Size: facts.size, SHA256: facts.sha256})
		tree.Entries = append(tree.Entries, domain.LadoSetupTreeEntry{Path: filepath.ToSlash(rel), Mode: treeMode(facts.mode), Size: facts.size, SHA256: facts.sha256})
		return nil
	})
	if err != nil {
		return nil, tree, err
	}
	// Copying into a subfolder changed its mtime; the deepest come last.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(filepath.Join(dst, filepath.FromSlash(dirs[i].Path)), dirs[i].MTime, dirs[i].MTime); err != nil {
			return nil, tree, err
		}
	}
	return files, tree, nil
}

// treeMode is a file's mode in a tree: exec when anyone may run it.
func treeMode(mode fs.FileMode) domain.LadoSetupFileMode {
	if mode.Perm()&0o111 != 0 {
		return domain.LadoSetupModeExec
	}
	return domain.LadoSetupModeFile
}

func modeText(mode fs.FileMode) string { return fmt.Sprintf("%04o", mode.Perm()) }

func parseMode(text string) (fs.FileMode, error) {
	n, err := strconv.ParseUint(text, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("mode %q is not octal permission bits", text)
	}
	return fs.FileMode(n), nil
}

// verifyBackup reads the backup back: manifest.json must be the bytes
// written, and every copy must hash to its manifest entry and to what the
// plan hashed.
func verifyBackup(ctx context.Context, dir string, written []byte, plan adoptionPlan) error {
	fail := func(be backupEntry, format string, args ...any) error {
		return &stageError{stage: stageBackup, root: be.Root, path: be.RelPath, err: fmt.Errorf("the backup does not match: "+format, args...)}
	}
	data, err := readRegularNoLink(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return &stageError{stage: stageBackup, err: err}
	}
	if !bytes.Equal(data, written) {
		return &stageError{stage: stageBackup, err: errors.New("the backup does not match: manifest.json reads back differently")}
	}
	var m backupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return &stageError{stage: stageBackup, err: err}
	}
	moves := plan.moves()
	if len(m.Entries) != len(moves) {
		return &stageError{stage: stageBackup, err: fmt.Errorf("the backup does not match: %d entries for %d moves", len(m.Entries), len(moves))}
	}
	for i, be := range m.Entries {
		pe := moves[i].entry
		if be.Tool != pe.Tool || be.Kind != pe.Kind || be.Name != pe.Name {
			return fail(be, "entry %d is %s %s %s, not %s", i, be.Tool, be.Kind, be.Name, pe.Name)
		}
		sum, err := checkBackupCopy(ctx, dir, be)
		if err != nil {
			return fail(be, "%v", err)
		}
		if sum != pe.SHA256 {
			return fail(be, "the copy's content hashes to %s, not what the preview hashed", sum)
		}
	}
	return nil
}

// checkBackupCopy reads the backup copy of an entry back: it must match the
// manifest. It returns the sha256 of the content a version holds: a file's
// bytes, a folder's tree, or the value of the key, which a keys entry's copy
// of its whole file must hold.
func checkBackupCopy(ctx context.Context, dir string, be backupEntry) (string, error) {
	copyPath := be.copyPath(dir)
	switch be.Type {
	case backupTypeFile:
		size, sum, err := fileSum(copyPath)
		if err != nil {
			return "", err
		}
		if sum != be.SHA256 || size != be.Size {
			return "", fmt.Errorf("the copy hashes to %s", sum)
		}
		return sum, nil
	case backupTypeDir:
		tree, err := backupTree(ctx, copyPath, be.Files)
		if err != nil {
			return "", err
		}
		sum, err := tree.SHA256()
		if err != nil || sum != be.SHA256 {
			return "", fmt.Errorf("the copied folder hashes to %s", sum)
		}
		return sum, nil
	case backupTypeKeys:
		file, err := readRegularNoLink(copyPath)
		if err != nil {
			return "", err
		}
		if sha256Hex(file) != be.SHA256 || int64(len(file)) != be.Size {
			return "", fmt.Errorf("the copied file hashes to %s", sha256Hex(file))
		}
		value, err := keyValue(be.Tool, be.Kind, be.Name, file)
		if err != nil {
			return "", err
		}
		sum := sha256Hex(value)
		if len(be.RemovedKeys) != 1 || sum != be.RemovedKeys[0].SHA256 {
			return "", fmt.Errorf("the copied value hashes to %s", sum)
		}
		return sum, nil
	}
	return "", fmt.Errorf("unknown type %q", be.Type)
}

// backupTree hashes a copied folder: it must hold exactly the manifest's
// files and subfolders, each file with its size and sha256. The tree takes
// the original modes from the manifest, since the copies are owner-only.
func backupTree(ctx context.Context, dir string, files []backupFile) (domain.LadoSetupTree, error) {
	var tree domain.LadoSetupTree
	want := make(map[string]backupFile, len(files))
	for _, f := range files {
		want[f.Path] = f
	}
	seen := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel := slashRel(dir, path)
		f, ok := want[rel]
		if !ok || f.Dir != d.IsDir() {
			return fmt.Errorf("%s is not in the manifest", rel)
		}
		seen++
		if f.Dir {
			return nil
		}
		size, sum, err := fileSum(path)
		if err != nil {
			return err
		}
		if size != f.Size || sum != f.SHA256 {
			return fmt.Errorf("%s hashes to %s", rel, sum)
		}
		mode, err := parseMode(f.Mode)
		if err != nil {
			return err
		}
		tree.Entries = append(tree.Entries, domain.LadoSetupTreeEntry{Path: rel, Mode: treeMode(mode), Size: size, SHA256: sum})
		return nil
	})
	if err != nil {
		return tree, err
	}
	if seen != len(want) {
		return tree, fmt.Errorf("%d of the manifest's %d files are in the copy", seen, len(want))
	}
	return tree, nil
}

// restoreCopy writes the backup copy of a file or folder entry back to its
// original path, which must be free, with the original modes and mtimes.
func restoreCopy(dir string, be backupEntry) error {
	src := be.copyPath(dir)
	mode, err := parseMode(be.Mode)
	if err != nil {
		return err
	}
	switch be.Type {
	case backupTypeFile:
		return restoreFile(src, be.Path, mode, be.MTime)
	case backupTypeDir:
		if err := os.Mkdir(be.Path, privateDir); err != nil {
			return err
		}
		for _, f := range be.Files {
			target := filepath.Join(be.Path, filepath.FromSlash(f.Path))
			fmode, err := parseMode(f.Mode)
			if err != nil {
				return err
			}
			if f.Dir {
				if err := os.Mkdir(target, privateDir); err != nil {
					return err
				}
				continue
			}
			if err := restoreFile(filepath.Join(src, filepath.FromSlash(f.Path)), target, fmode, f.MTime); err != nil {
				return err
			}
		}
		// Folders get their modes and mtimes last, the deepest first.
		for i := len(be.Files) - 1; i >= 0; i-- {
			if f := be.Files[i]; f.Dir {
				fmode, _ := parseMode(f.Mode)
				target := filepath.Join(be.Path, filepath.FromSlash(f.Path))
				if err := os.Chmod(target, fmode); err != nil {
					return err
				}
				if err := os.Chtimes(target, f.MTime, f.MTime); err != nil {
					return err
				}
			}
		}
		if err := os.Chmod(be.Path, mode); err != nil {
			return err
		}
		return os.Chtimes(be.Path, be.MTime, be.MTime)
	}
	return fmt.Errorf("%s is a %s entry, not a file or a folder", be.RelPath, be.Type)
}

// restoreFile copies the backup copy src to a new file at dst with mode and
// mtime.
func restoreFile(src, dst string, mode fs.FileMode, mtime time.Time) error {
	data, err := readRegularNoLink(src)
	if err != nil {
		return err
	}
	if err := writeNew(dst, data, mode); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}

// readRegularNoLink reads the regular file at path, which must not be a
// link.
func readRegularNoLink(path string) ([]byte, error) {
	f, err := openRegular(path, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// writeNew writes data to a new file at path with mode, flushed.
func writeNew(path string, data []byte, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		// The umask may have taken bits off.
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// atomicWrite replaces the file at path with data through a new file in the
// same folder and a rename, so path holds the old bytes or the new ones and
// never a part. The new file gets mode. rename is os.Rename, or a test's
// stand-in.
func atomicWrite(path string, data []byte, mode fs.FileMode, rename func(oldpath, newpath string) error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".lado-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}

// syncTree flushes every folder under dir, dir included.
func syncTree(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return syncDir(path)
	})
}

// syncDir flushes a folder's entries. Windows cannot open a folder to flush
// it and writes them through.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
