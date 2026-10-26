package ladosetup

// Restore (plan §8, docs/LJY-PRD/HARNESS.md §9.2): the way back from an
// adoption. Each original the adoption removed comes back from its backup. A
// file or folder is written byte for byte with its modes and mtimes where its
// path is free, and beside what holds the path now, as <name>.lado-restored,
// where it is taken. A key goes back into its file only where the file has
// none; a file that holds exactly what the adoption left comes back whole,
// and one that is gone comes back with the adopted keys alone, so login keys
// and app state the developer removed with it stay removed. Restore never
// overwrites or removes what it finds, so
// running it again after a failure finishes the rest by the same rules. The
// adoption's items then end: they are no longer resolved or delivered, and
// their versions, the managed copies and the backup stay.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// Error codes of restore.
const (
	// CodeAlreadyRestored is an adoption that was restored before.
	CodeAlreadyRestored = "LADO_SETUP_ALREADY_RESTORED"
	// CodeRestoreFailed is a restore that stopped; details name the stage
	// and, when an original could not be put back, the first one.
	CodeRestoreFailed = "LADO_SETUP_RESTORE_FAILED"
)

// What restore did with an original, in its report.
const (
	// RestoreRestored put the original back where it was, or found it there.
	RestoreRestored = "restored"
	// RestoreBeside wrote the original beside what holds its path now.
	RestoreBeside = "restored_beside"
	// RestoreKeyKept left the key the file has now in place of the
	// original's.
	RestoreKeyKept = "key_kept"
	// RestoreFailed could not put the original back; Error says why.
	RestoreFailed = "failed"
)

// Restore stages, in a failure's details.
const (
	restoreStageManifest = "manifest"
	restoreStagePutBack  = "put_back"
	restoreStageRecord   = "record"
)

const (
	restoreReportFormat = "lado-setup-restore/1"
	actionRestored      = "setup.restored"
	// besideSuffix names the copy restore writes beside a taken path. The
	// next copy beside the same path is <name>.lado-restored-2, up to
	// maxBeside.
	besideSuffix = ".lado-restored"
	maxBeside    = 99
)

var errRestoredDiffers = errors.New("the restored copy does not hash to its backup")

// RestoreReport is what a restore did, kept with the adoption.
type RestoreReport struct {
	Format     string                     `json:"format"`
	AdoptionID domain.LadoSetupAdoptionID `json:"adoptionId"`
	// Entries are the originals of the adoption's items, in backup order.
	Entries []RestoredEntry `json:"entries"`
}

// RestoredEntry is the original of one item and what restore did with it.
type RestoredEntry struct {
	ItemID domain.LadoSetupItemID `json:"itemId"`
	Tool   domain.LadoSetupTool   `json:"tool"`
	Kind   domain.LadoSetupKind   `json:"kind"`
	Name   string                 `json:"name"`
	// Root and Path place the original under its root, with slashes and
	// "#<key>" after a key's file, and Beside the copy written beside it.
	// Neither names the developer's home.
	Root   domain.LadoSetupLocation `json:"root"`
	Path   string                   `json:"path"`
	Status string                   `json:"status"`
	Beside string                   `json:"beside,omitempty"`
	Error  string                   `json:"error,omitempty"`
}

// RestoreResult is a restore.
type RestoreResult struct {
	AdoptionID domain.LadoSetupAdoptionID
	// Applied is set when the store recorded the restore.
	Applied    bool
	RestoredAt time.Time
	Report     RestoreReport
	Warnings   []string
}

// restoreTarget is an adoption to restore with its backup.
type restoreTarget struct {
	adoption domain.LadoSetupAdoption
	dir      string
	// entries are the backup entries of the items the adoption recorded, in
	// backup order. A partial adoption recorded the entries it removed.
	entries []restoreEntry
}

// restoreEntry is a backup entry with the item the adoption recorded for it.
type restoreEntry struct {
	entry backupEntry
	item  domain.LadoSetupItem
}

// Restore puts back what an adoption removed, after the desktop app's
// confirmation for the adoption id. Like adoption it waits until no Claude
// Code or Codex session or program runs, but it runs on every OS: the way
// back stays open where adoption is refused. A restore that could not put
// back every original records nothing and returns its report with the
// error; running it again finishes the rest.
func (s *Service) Restore(ctx context.Context, id domain.LadoSetupAdoptionID, presence func(action, subject string) (domain.LadoPresenceProof, error)) (RestoreResult, error) {
	if err := s.available(); err != nil {
		return RestoreResult{}, err
	}
	if err := s.lockWrite(ctx); err != nil {
		return RestoreResult{}, err
	}
	defer s.unlockWrite()
	t, err := s.restoreTarget(ctx, id)
	if err != nil {
		return RestoreResult{}, err
	}
	ap, pre, err := s.confirmRestore(ctx, t, presence)
	if err != nil {
		return RestoreResult{}, err
	}
	// From the first write on, the restore runs to its end.
	ctx = context.WithoutCancel(ctx)
	report, written := s.putBackOriginals(ctx, t)
	res := RestoreResult{AdoptionID: id, Report: report}
	for _, e := range report.Entries {
		if e.Status == RestoreFailed {
			s.rescan(ctx)
			return res, s.restoreFailed(id, e)
		}
	}
	at := s.now()
	if err := s.recordRestore(ctx, t, report, at, ap, pre); err != nil {
		s.rescan(ctx)
		return res, err
	}
	res.Applied, res.RestoredAt = true, at
	if err := s.addToBaseline(ctx, written); err != nil {
		s.deps.Log.Warn("LADO could not save the setup drift baseline", "err", err)
		res.Warnings = append(res.Warnings, WarningBaselineFailed)
		s.rescan(ctx)
	}
	return res, nil
}

// restoreTarget reads an adoption that was not restored and its backup,
// whose manifest must be the one the adoption recorded.
func (s *Service) restoreTarget(ctx context.Context, id domain.LadoSetupAdoptionID) (restoreTarget, error) {
	adoption, ok, err := s.deps.Store.LadoSetupAdoption(ctx, id)
	if err != nil {
		return restoreTarget{}, fmt.Errorf("read the adoption: %w", err)
	}
	if !ok {
		return restoreTarget{}, apierr.NotFound(CodeNotFound, "LADO has no such adoption.")
	}
	if !adoption.RestoredAt.IsZero() {
		return restoreTarget{}, alreadyRestored(id)
	}
	dir, m, err := s.readBackupManifest(adoption)
	if err != nil {
		return restoreTarget{}, s.backupUnreadable(id, err)
	}
	items, err := s.deps.Store.ListLadoSetupItemsForAdoption(ctx, id)
	if err != nil {
		return restoreTarget{}, fmt.Errorf("list the adoption's items: %w", err)
	}
	type name struct {
		tool domain.LadoSetupTool
		kind domain.LadoSetupKind
		name string
	}
	recorded := map[name]domain.LadoSetupItem{}
	for _, it := range items {
		recorded[name{it.Tool, it.Kind, it.Name}] = it
	}
	t := restoreTarget{adoption: adoption, dir: dir}
	for _, be := range m.Entries {
		key := name{be.Tool, be.Kind, be.Name}
		if it, ok := recorded[key]; ok {
			t.entries = append(t.entries, restoreEntry{entry: be, item: it})
			delete(recorded, key)
		}
	}
	if len(recorded) > 0 {
		return restoreTarget{}, s.backupUnreadable(id, fmt.Errorf("the backup has no original of %d of the adoption's items", len(recorded)))
	}
	return t, nil
}

// readBackupManifest reads an adoption's backup folder and manifest, which
// must be the one the adoption recorded.
func (s *Service) readBackupManifest(adoption domain.LadoSetupAdoption) (string, backupManifest, error) {
	dir := backupDir(s.deps.StateDir, adoption.ID)
	data, err := readRegularNoLink(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", backupManifest{}, err
	}
	if sha256Hex(data) != adoption.ManifestSHA256 {
		return "", backupManifest{}, errors.New("the backup manifest is not the one the adoption recorded")
	}
	var m backupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", backupManifest{}, err
	}
	if m.Format != backupFormat || m.AdoptionID != adoption.ID {
		return "", backupManifest{}, fmt.Errorf("the backup manifest is %q of %s", m.Format, m.AdoptionID)
	}
	return dir, m, nil
}

// confirmRestore checks what holds the restore back, then takes the desktop
// app's confirmation. An OS where adoption is refused does not hold it back.
func (s *Service) confirmRestore(ctx context.Context, t restoreTarget, presence func(action, subject string) (domain.LadoPresenceProof, error)) (approval, Preconditions, error) {
	var tools []domain.LadoSetupTool
	for _, re := range t.entries {
		if !slices.Contains(tools, re.item.Tool) {
			tools = append(tools, re.item.Tool)
		}
	}
	slices.Sort(tools)
	pre, err := s.preconditions(ctx, tools)
	if err != nil {
		return approval{}, Preconditions{}, err
	}
	if len(pre.Sessions) > 0 || len(pre.Processes) > 0 {
		return approval{}, Preconditions{}, apierr.Conflict(CodeToolRunning, "Close the Claude Code and Codex sessions and programs listed, then restore again.", runningDetails(pre))
	}
	if presence == nil {
		return approval{}, Preconditions{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Restore your local setup in the AO desktop app. It asks you to confirm, then sends the restore itself.")
	}
	proof, err := presence(domain.LadoPresenceSetupRestore, string(t.adoption.ID))
	if err != nil {
		return approval{}, Preconditions{}, err
	}
	return approvalOf(proof), pre, nil
}

// putBackOriginals puts back every original of the target, files and
// folders one by one and keys a file at a time, and goes on past a failure.
// It returns the report and the inventory paths where it put setup, "<file>#"
// for a key file it wrote whole.
func (s *Service) putBackOriginals(ctx context.Context, t restoreTarget) (RestoreReport, map[string]bool) {
	report := RestoreReport{Format: restoreReportFormat, AdoptionID: t.adoption.ID, Entries: make([]RestoredEntry, len(t.entries))}
	written := map[string]bool{}
	var files []string
	keys := map[string][]int{}
	for i, re := range t.entries {
		be := re.entry
		out := &report.Entries[i]
		*out = RestoredEntry{ItemID: re.item.ID, Tool: be.Tool, Kind: be.Kind, Name: be.Name, Root: be.Root, Path: be.RelPath}
		if be.Type != backupTypeKeys {
			s.putBackFile(ctx, t.dir, be, out, written)
			continue
		}
		out.Path += "#" + keySuffix(be)
		if _, ok := keys[be.Path]; !ok {
			files = append(files, be.Path)
		}
		keys[be.Path] = append(keys[be.Path], i)
	}
	for _, file := range files {
		group := make([]backupEntry, 0, len(keys[file]))
		out := make([]*RestoredEntry, 0, len(keys[file]))
		for _, i := range keys[file] {
			group = append(group, t.entries[i].entry)
			out = append(out, &report.Entries[i])
		}
		s.putBackKeys(ctx, t.dir, group, out, written)
	}
	return report, written
}

// putBackFile puts back a file or folder original: at its path when that is
// free or holds the original already, else beside it.
func (s *Service) putBackFile(ctx context.Context, dir string, be backupEntry, out *RestoredEntry, written map[string]bool) {
	fail := func(err error) { out.Status, out.Error = RestoreFailed, plainError(err) }
	if _, err := checkBackupCopy(ctx, dir, be); err != nil {
		fail(err)
		return
	}
	// The original's path first; when it is taken, a copy beside it that an
	// earlier restore wrote, else the first free name beside it.
	at, free := -1, -1
	for n := 0; n <= maxBeside && at < 0; n++ {
		state, err := originalAt(ctx, besidePath(be.Path, n), be)
		switch {
		case err != nil:
			fail(err)
			return
		case state == pathOriginal:
			at = n
		case state == pathFree && n == 0:
			at, free = 0, 0
		case state == pathFree && free < 0:
			free = n
		}
	}
	if at < 0 {
		if free < 0 {
			fail(fmt.Errorf("%d copies are beside it already", maxBeside))
			return
		}
		at = free
	}
	target := besidePath(be.Path, at)
	if at == free {
		if err := s.writeBackupCopy(ctx, dir, be, target); err != nil {
			fail(err)
			return
		}
	}
	out.Status = RestoreRestored
	if at > 0 {
		out.Status, out.Beside = RestoreBeside, besidePath(be.RelPath, at)
	}
	written[target] = true
}

// besidePath is path for n = 0, and the nth copy beside it after that.
func besidePath(path string, n int) string {
	switch n {
	case 0:
		return path
	case 1:
		return path + besideSuffix
	}
	return path + besideSuffix + "-" + strconv.Itoa(n)
}

// pathState is what a path holds for a file or folder original.
type pathState int

const (
	// pathFree holds nothing.
	pathFree pathState = iota
	// pathOriginal holds the backed-up original: its file's bytes, or its
	// folder's tree.
	pathOriginal
	// pathTaken holds anything else, or something LADO cannot read.
	pathTaken
)

// originalAt tells what path holds for the original of be. It reads without
// following links.
func originalAt(ctx context.Context, path string, be backupEntry) (pathState, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pathFree, nil
	case err != nil:
		return 0, err
	}
	var sum string
	switch {
	case be.Type == backupTypeDir && info.IsDir():
		tree, err := treeOf(ctx, path)
		if err != nil {
			return pathTaken, nil //nolint:nilerr // an unreadable folder is not the original
		}
		if sum, err = tree.SHA256(); err != nil {
			return pathTaken, nil //nolint:nilerr // a tree LADO cannot hash is not the original
		}
	case be.Type == backupTypeFile && info.Mode().IsRegular():
		if _, sum, err = fileSum(path); err != nil {
			return pathTaken, nil //nolint:nilerr // an unreadable file is not the original
		}
	default:
		return pathTaken, nil
	}
	if sum == be.SHA256 {
		return pathOriginal, nil
	}
	return pathTaken, nil
}

// writeBackupCopy writes the backup copy of a file or folder entry at
// target, which is free, with the original's modes and mtimes. The copy is
// written as a new sibling, checked against the manifest and renamed into
// place, so target never holds part of it or other bytes. A folder missing
// on the way is made owner-only.
func (s *Service) writeBackupCopy(ctx context.Context, dir string, be backupEntry, target string) error {
	staged := be
	staged.Path = s.stagedPath(target)
	if err := os.MkdirAll(filepath.Dir(target), privateDir); err != nil {
		return err
	}
	err := restoreCopy(dir, staged)
	if err == nil {
		var state pathState
		if state, err = originalAt(ctx, staged.Path, be); err == nil && state != pathOriginal {
			err = errRestoredDiffers
		}
	}
	if err != nil {
		_ = os.RemoveAll(staged.Path)
		return err
	}
	return placeStaged(staged.Path, target)
}

// writeNewFile writes data to a new file at path, which is free, with mode,
// through a new sibling and a rename.
func (s *Service) writeNewFile(path string, data []byte, mode fs.FileMode) error {
	staged := s.stagedPath(path)
	if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
		return err
	}
	if err := writeNew(staged, data, mode); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return placeStaged(staged, path)
}

// stagedPath is a new name beside target for writing it.
func (s *Service) stagedPath(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".lado-restoring-"+s.deps.NewID())
}

// placeStaged renames a staged copy to target when target is still free.
// What took target meanwhile is not replaced, and the staged copy goes.
// Between the check and the rename something could still take it: no
// Claude Code or Codex runs during a restore (plan decision h).
func placeStaged(staged, target string) error {
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		_ = os.RemoveAll(staged)
		return cmp.Or(err, errors.New("something took its place while LADO wrote it"))
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	return syncDir(filepath.Dir(target))
}

// replaceFile writes data over the file at path through a new file and a
// rename, when the file still holds was; else it writes nothing.
func replaceFile(path string, was, data []byte, mode fs.FileMode) error {
	now, err := readRegularNoLink(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(now, was) {
		return errors.New("it changed while LADO restored it")
	}
	return atomicWrite(path, data, mode, os.Rename)
}

// putBackKeys puts back the keys an adoption took out of one file, group in
// backup order. A file that holds exactly what the adoption left comes back
// whole: the backup is then the file as it was, every key in its place.
// Otherwise each key goes back only where the file has none, beside the keys
// it followed, and a key the file has now stays. A file that is gone comes
// back with the adopted keys alone.
func (s *Service) putBackKeys(ctx context.Context, dir string, group []backupEntry, out []*RestoredEntry, written map[string]bool) {
	fail := func(k int, err error) { out[k].Status, out[k].Error = RestoreFailed, plainError(err) }
	failAll := func(err error) {
		for k := range group {
			fail(k, err)
		}
	}
	for k, be := range group {
		if _, err := checkBackupCopy(ctx, dir, be); err != nil {
			fail(k, err)
		}
	}
	first := group[0]
	path := first.Path
	orig, err := readRegularNoLink(first.copyPath(dir))
	if err == nil && sha256Hex(orig) != first.SHA256 {
		err = errors.New("the backup copy changed")
	}
	if err != nil {
		failAll(err)
		return
	}
	mode, err := parseMode(first.Mode)
	if err != nil {
		failAll(err)
		return
	}
	// done marks the keys in place, and where restore put setup: the keys
	// it restored, and every key of a file it wrote whole.
	done := func(whole bool) {
		for k, be := range group {
			if whole && out[k].Status == "" {
				out[k].Status = RestoreRestored
			}
			if out[k].Status == RestoreRestored {
				written[path+"#"+keySuffix(be)] = true
			}
		}
		if whole {
			written[path+"#"] = true
		}
	}
	cur, err := readRegularNoLink(path)
	gone := errors.Is(err, fs.ErrNotExist)
	switch {
	case gone:
		cur = nil
	case err != nil:
		failAll(err)
		return
	default:
		if left, err := adoptionLeft(group, orig); err == nil && bytes.Equal(cur, left) {
			err := replaceFile(path, cur, orig, mode)
			if err == nil {
				err = os.Chtimes(path, first.MTime, first.MTime)
			}
			if err == nil {
				if _, sum, serr := fileSum(path); serr != nil || sum != first.SHA256 {
					err = cmp.Or(serr, errRestoredDiffers)
				}
			}
			if err != nil {
				failAll(err)
				return
			}
			done(true)
			return
		}
	}
	next := cur
	var inserted []int
	for k, be := range group {
		if out[k].Status == RestoreFailed {
			continue
		}
		status, edited, err := putKeyBack(be, orig, next)
		if err != nil {
			fail(k, err)
			continue
		}
		if !bytes.Equal(edited, next) {
			inserted = append(inserted, k)
		}
		next, out[k].Status = edited, status
	}
	if len(inserted) > 0 {
		var err error
		if gone {
			err = s.writeNewFile(path, next, mode)
		} else {
			var info fs.FileInfo
			if info, err = os.Lstat(path); err == nil {
				err = replaceFile(path, cur, next, info.Mode().Perm())
			}
		}
		if err != nil {
			for _, k := range inserted {
				fail(k, err)
			}
		}
	}
	final, err := readRegularNoLink(path)
	for k, be := range group {
		if out[k].Status == RestoreRestored && (err != nil || !keyInPlace(be, orig, final)) {
			fail(k, cmp.Or(err, errRestoredDiffers))
		}
	}
	done(false)
}

// adoptionLeft is what a key file held after the adoption took group's keys
// out of orig, as the adoption took them: one key at a time, in backup order.
func adoptionLeft(group []backupEntry, orig []byte) ([]byte, error) {
	left := orig
	for _, be := range group {
		var err error
		if left, err = withoutAdoptedKey(be, left); err != nil {
			return nil, err
		}
	}
	return left, nil
}

// putKeyBack puts the key of be, as orig holds it, into cur when cur has no
// key of that name, and says what it did.
func putKeyBack(be backupEntry, orig, cur []byte) (string, []byte, error) {
	has, err := hasKey(be, cur)
	switch {
	case err != nil:
		return "", nil, err
	case !has:
		out, err := withOriginalKey(be, orig, cur)
		return RestoreRestored, out, err
	case isEnvSetting(be):
		return mergeEnv(be, orig, cur)
	case keyInPlace(be, orig, cur):
		return RestoreRestored, cur, nil
	}
	return RestoreKeyKept, cur, nil
}

func isEnvSetting(be backupEntry) bool {
	return be.Tool == domain.LadoSetupToolClaudeCode && be.Kind == domain.LadoSetupKindSetting && be.Name == claudeEnvKey
}

// keySuffix names an entry's key after "#" in its file's inventory path.
func keySuffix(be backupEntry) string {
	switch {
	case be.Tool == domain.LadoSetupToolCodex:
		return codexServersKey + "." + be.Name
	case be.Kind == domain.LadoSetupKindMCPServer:
		return claudeMCPKey + "." + be.Name
	}
	return be.Name
}

// hasKey reports whether file has a key of be's name, whatever its value.
func hasKey(be backupEntry, file []byte) (bool, error) {
	switch {
	case be.Tool == domain.LadoSetupToolCodex:
		var doc map[string]any
		if err := toml.Unmarshal(file, &doc); err != nil {
			return false, err
		}
		servers, _ := doc[codexServersKey].(map[string]any)
		_, ok := servers[be.Name]
		return ok, nil
	case be.Kind == domain.LadoSetupKindMCPServer:
		servers, ok, err := jsonMemberValue(file, claudeMCPKey)
		if err != nil || !ok {
			return false, err
		}
		_, ok, err = jsonMemberValue(servers, be.Name)
		return ok, err
	}
	_, ok, err := jsonMemberValue(file, be.Name)
	return ok, err
}

// keyInPlace reports whether file holds be's key as the adoption took it. The
// env setting holds it when every variable the adoption took has its value
// there; other variables may be there too.
func keyInPlace(be backupEntry, orig, file []byte) bool {
	if isEnvSetting(be) {
		if adopted, ok := adoptedEnv(orig); ok {
			value, has, err := jsonMemberValue(file, claudeEnvKey)
			if err != nil || !has {
				return false
			}
			current, err := jsonMembers(value)
			if err != nil {
				return false
			}
			for _, m := range adopted {
				j := slices.IndexFunc(current, func(c jsonMember) bool { return c.Key == m.Key })
				if j < 0 || !sameJSON(current[j].Value, m.Value) {
					return false
				}
			}
			return true
		}
	}
	value, err := keyValue(be.Tool, be.Kind, be.Name, file)
	return err == nil && len(be.RemovedKeys) == 1 && sha256Hex(value) == be.RemovedKeys[0].SHA256
}

// withOriginalKey sets be's key, which cur does not have, as orig holds it:
// a Codex server's lines at the end of config.toml, a Claude server among
// mcpServers, a setting among the other keys. The env setting gets the
// variables the adoption took, not the login ones that stayed.
func withOriginalKey(be backupEntry, orig, cur []byte) ([]byte, error) {
	switch {
	case be.Tool == domain.LadoSetupToolCodex:
		_, block, err := tomlWithoutServer(orig, be.Name)
		if err != nil {
			return nil, err
		}
		return tomlWithBlock(cur, block)
	case be.Kind == domain.LadoSetupKindMCPServer:
		servers, _, err := jsonMemberValue(orig, claudeMCPKey)
		if err != nil {
			return nil, err
		}
		value, ok, err := jsonMemberValue(servers, be.Name)
		if err != nil || !ok {
			return nil, cmp.Or(err, fmt.Errorf("the backup has no MCP server %s", be.Name))
		}
		now, has, err := jsonMemberValue(cur, claudeMCPKey)
		if err != nil {
			return nil, err
		}
		if !has {
			object, err := formatJSONObject([]jsonMember{{Key: be.Name, Value: value}}, false)
			if err != nil {
				return nil, err
			}
			return jsonInsert(cur, claudeMCPKey, object, memberKeys(orig))
		}
		merged, err := jsonInsert(now, be.Name, value, memberKeys(servers))
		if err != nil {
			return nil, fmt.Errorf("the value of %s: %w", claudeMCPKey, err)
		}
		return jsonWith(cur, claudeMCPKey, merged)
	}
	value, ok, err := jsonMemberValue(orig, be.Name)
	if err != nil || !ok {
		return nil, cmp.Or(err, fmt.Errorf("the backup has no setting %s", be.Name))
	}
	if isEnvSetting(be) {
		if adopted, ok := adoptedEnv(orig); ok {
			if value, err = formatJSONObject(adopted, false); err != nil {
				return nil, err
			}
		}
	}
	return jsonInsert(cur, be.Name, value, memberKeys(orig))
}

// mergeEnv puts the variables the adoption took out of the env setting back
// into the env cur has now, where it has none of that name. A variable it
// has with another value stays, and the setting is then kept.
func mergeEnv(be backupEntry, orig, cur []byte) (string, []byte, error) {
	adopted, ok := adoptedEnv(orig)
	if !ok {
		if keyInPlace(be, orig, cur) {
			return RestoreRestored, cur, nil
		}
		return RestoreKeyKept, cur, nil
	}
	now, _, err := jsonMemberValue(cur, claudeEnvKey)
	if err != nil {
		return "", nil, err
	}
	current, err := jsonMembers(now)
	if err != nil {
		// No object now: it stays as it is.
		return RestoreKeyKept, cur, nil //nolint:nilerr // the developer's value is kept
	}
	origEnv, _, _ := jsonMemberValue(orig, claudeEnvKey)
	order := memberKeys(origEnv)
	status := RestoreRestored
	merged := slices.Clone(current)
	for _, m := range adopted {
		j := slices.IndexFunc(merged, func(c jsonMember) bool { return c.Key == m.Key })
		switch {
		case j < 0:
			merged = insertMember(merged, m, order)
		case !sameJSON(merged[j].Value, m.Value):
			status = RestoreKeyKept
		}
	}
	if len(merged) == len(current) {
		return status, cur, nil
	}
	env, err := formatJSONObject(merged, false)
	if err != nil {
		return "", nil, err
	}
	out, err := jsonWith(cur, claudeEnvKey, env)
	return status, out, err
}

// adoptedEnv is what an adoption took of the env setting in orig: its
// variables without the login ones, in their order. ok is false when the
// setting is no object, which the adoption took as it was.
func adoptedEnv(orig []byte) ([]jsonMember, bool) {
	value, has, err := jsonMemberValue(orig, claudeEnvKey)
	if err != nil || !has {
		return nil, false
	}
	if trimmed := bytes.TrimSpace(value); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	vars, err := jsonMembers(value)
	if err != nil {
		return nil, false
	}
	return slices.DeleteFunc(vars, func(m jsonMember) bool { return slices.Contains(domain.LadoSetupClaudeLoginEnv, m.Key) }), true
}

// sameJSON reports whether two JSON values are equal as stored.
func sameJSON(a, b []byte) bool {
	ca, err := canonicalJSON(a)
	if err != nil {
		return false
	}
	cb, err := canonicalJSON(b)
	return err == nil && bytes.Equal(ca, cb)
}

// recordRestore stores the restore with its report, the presence proof and
// the audit event; the adoption's items end with it.
func (s *Service) recordRestore(ctx context.Context, t restoreTarget, report RestoreReport, at time.Time, ap approval, pre Preconditions) error {
	id := t.adoption.ID
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, e := range report.Entries {
		counts[e.Status]++
	}
	payload, _ := json.Marshal(map[string]any{
		"items":            len(report.Entries),
		"restored":         counts[RestoreRestored],
		"restoredBeside":   counts[RestoreBeside],
		"keyKept":          counts[RestoreKeyKept],
		"processesIgnored": pre.ProcessesIgnored,
		"proof":            ap.proof.Nonce,
		"dev":              ap.proof.Dev,
	})
	event := domain.LadoAuditEvent{At: at, Actor: ap.actor, Action: actionRestored, SubjectKind: subjectAdoption, SubjectID: string(id), Payload: payload}
	proof := ap.proof
	err = s.deps.Store.RestoreLadoSetupAdoption(ctx, id, data, at, event, &proof)
	switch {
	case errors.Is(err, domain.ErrLadoSetupAlreadyRestored):
		return alreadyRestored(id)
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return ladopresence.Replayed()
	case err != nil:
		s.deps.Log.Error("LADO put back an adoption's originals but could not record the restore", "adoption", id, "err", err)
		return apierr.New(apierr.KindInternal, CodeRestoreFailed, "LADO put back the originals but could not record the restore. Restore again to record it.",
			map[string]any{"adoption": string(id), "stage": restoreStageRecord})
	}
	return nil
}

func alreadyRestored(id domain.LadoSetupAdoptionID) error {
	return apierr.Conflict(CodeAlreadyRestored, "This adoption was restored before.", map[string]any{"adoption": string(id)})
}

// backupUnreadable is the error of a restore that stopped before it wrote
// anything: the backup is not the one the adoption recorded.
func (s *Service) backupUnreadable(id domain.LadoSetupAdoptionID, err error) error {
	s.deps.Log.Error("LADO setup restore cannot read the adoption's backup", "adoption", id, "err", err)
	return apierr.New(apierr.KindInternal, CodeRestoreFailed, "LADO cannot read this adoption's backup as it was written, so it restored nothing.",
		map[string]any{"adoption": string(id), "stage": restoreStageManifest, "error": plainError(err)})
}

// restoreFailed is the error of a restore that could not put back every
// original, naming the first.
func (s *Service) restoreFailed(id domain.LadoSetupAdoptionID, e RestoredEntry) error {
	s.deps.Log.Error("LADO setup restore could not put back an original", "adoption", id, "path", e.Path, "err", e.Error)
	return apierr.New(apierr.KindInternal, CodeRestoreFailed,
		"LADO could not put back every original. What it put back stays where it is; restore again to finish the rest.",
		map[string]any{
			"adoption": string(id), "stage": restoreStagePutBack, "tool": string(e.Tool), "kind": string(e.Kind), "name": e.Name,
			"root": string(e.Root), "path": e.Path, "error": e.Error,
		})
}

// addToBaseline puts what now sits at paths, where restore put setup, into
// the drift baseline, so it is not new local setup. A path "<file>#" stands
// for every key of a file restore wrote whole. Without a baseline it saves a
// whole one, as adoption does.
func (s *Service) addToBaseline(ctx context.Context, paths map[string]bool) error {
	b, err := readBaseline(s.deps.StateDir)
	if err != nil {
		return s.saveBaseline(ctx)
	}
	inv, err := s.Scan(ctx)
	if err != nil {
		return err
	}
	restored := func(path string) bool {
		if paths[path] {
			return true
		}
		file, _, ok := strings.Cut(path, "#")
		return ok && paths[file+"#"]
	}
	b.Entries = slices.DeleteFunc(b.Entries, func(e baselineEntry) bool { return restored(e.Path) })
	for _, e := range inv.Entries {
		if isOriginal(e.Location) && e.Status == domain.LadoSetupStatusAdoptable && restored(e.Path) {
			b.Entries = append(b.Entries, baselineOf(e))
		}
	}
	b.SavedAt = inv.ScannedAt
	return s.setBaseline(ctx, b)
}
