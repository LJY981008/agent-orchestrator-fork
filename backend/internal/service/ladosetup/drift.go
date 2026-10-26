package ladosetup

// Drift (plan §8, docs/LJY-PRD/HARNESS.md §9.6): what changed outside LADO.
// A managed copy that no longer holds its item's latest version is edited;
// an adoptable entry at an original location that the drift baseline does
// not hold, or holds with other content, is new local setup. Drift is only
// shown: sessions get stored versions, so it never changes what they load,
// and new local setup is never moved by itself. Keeping an edit makes the
// managed content the item's next version; reverting writes the latest
// version over the managed copy.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// Error codes of drift.
const (
	// CodeNotFound is an adoption or an item LADO does not have, or an item
	// that is no longer live.
	CodeNotFound = "LADO_SETUP_NOT_FOUND"
	// CodeItemChanged is an item that got another version while a change of
	// it was made.
	CodeItemChanged = "LADO_SETUP_ITEM_CHANGED"
	// CodeDriftUnreadable is a managed copy LADO cannot read as its item's
	// content, such as a file that is gone or a folder that holds a link.
	// It can be reverted, not kept.
	CodeDriftUnreadable = "LADO_SETUP_DRIFT_UNREADABLE"
)

// Audit of drift.
const (
	actorAPI            = "api"
	actionDriftKept     = "setup.drift_kept"
	actionDriftReverted = "setup.drift_reverted"
	subjectItem         = "setup_item"
)

// Drift scans again and returns what changed outside LADO. The scan renews
// the cached inventory, whose Drift it is.
func (s *Service) Drift(ctx context.Context) ([]domain.LadoSetupDrift, error) {
	inv, err := s.Scan(ctx)
	if err != nil {
		return nil, err
	}
	return inv.Drift, nil
}

// editedDrift lists the live items whose managed copy no longer holds their
// latest version. A copy LADO cannot read has no sha256.
func (s *Service) editedDrift(ctx context.Context, items []domain.LadoSetupItemWithVersion) []domain.LadoSetupDrift {
	if s.deps.StateDir == "" {
		return nil
	}
	var out []domain.LadoSetupDrift
	for _, iv := range items {
		sum, err := s.managedSum(ctx, iv.Item)
		if err == nil && s.compareManaged(iv, sum) == nil {
			continue
		}
		if err != nil {
			sum = ""
		}
		it := iv.Item
		out = append(out, domain.LadoSetupDrift{
			ItemID: it.ID, Kind: domain.LadoSetupDriftEdited, Tool: it.Tool, ItemKind: it.Kind, Name: it.Name,
			Path: managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name), SHA256: sum,
		})
	}
	return out
}

// newLocalDrift lists the adoptable entries at the original locations that
// baseline, sha256 by inventory path, does not hold with their content. A
// new entry with the name of a live item names that item. Without a
// baseline, before the first adoption, nothing is new.
func newLocalDrift(entries []domain.LadoSetupEntry, baseline map[string]string) []domain.LadoSetupDrift {
	if baseline == nil {
		return nil
	}
	type name struct {
		tool domain.LadoSetupTool
		kind domain.LadoSetupKind
		name string
	}
	live := map[name]domain.LadoSetupItemID{}
	for _, e := range entries {
		if e.Location == domain.LadoSetupLocationManaged {
			live[name{e.Tool, e.Kind, e.Name}] = e.ItemID
		}
	}
	var out []domain.LadoSetupDrift
	for _, e := range entries {
		if !isOriginal(e.Location) || e.Status != domain.LadoSetupStatusAdoptable {
			continue
		}
		if sum, ok := baseline[e.Path]; ok && sum == e.SHA256 {
			continue
		}
		out = append(out, domain.LadoSetupDrift{
			ItemID: live[name{e.Tool, e.Kind, e.Name}], Kind: domain.LadoSetupDriftNewLocal, Tool: e.Tool, ItemKind: e.Kind, Name: e.Name,
			Path: e.Path, SHA256: e.SHA256,
		})
	}
	return out
}

// editedOnly keeps the edits of a drift list.
func editedOnly(drift []domain.LadoSetupDrift) []domain.LadoSetupDrift {
	var out []domain.LadoSetupDrift
	for _, d := range drift {
		if d.Kind == domain.LadoSetupDriftEdited {
			out = append(out, d)
		}
	}
	return out
}

func baselineOf(e domain.LadoSetupEntry) baselineEntry {
	return baselineEntry{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Path: e.Path, SHA256: e.SHA256}
}

// readBaseline reads the drift baseline. Before the first adoption there is
// none (fs.ErrNotExist).
func readBaseline(stateDir string) (setupBaseline, error) {
	data, err := readRegularNoLink(baselinePath(stateDir))
	if err != nil {
		return setupBaseline{}, err
	}
	var b setupBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return setupBaseline{}, err
	}
	if b.Format != baselineFormat {
		return setupBaseline{}, fmt.Errorf("the drift baseline is %q, not %q", b.Format, baselineFormat)
	}
	return b, nil
}

// baselineSums is the drift baseline as sha256 by inventory path, or nil
// when there is none. One that cannot be read is logged and counts as none.
func (s *Service) baselineSums() map[string]string {
	if s.deps.StateDir == "" {
		return nil
	}
	b, err := readBaseline(s.deps.StateDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.deps.Log.Warn("LADO could not read the setup drift baseline", "err", err)
		}
		return nil
	}
	sums := make(map[string]string, len(b.Entries))
	for _, e := range b.Entries {
		sums[e.Path] = e.SHA256
	}
	return sums
}

// setBaseline saves the drift baseline and checks the cached inventory's new
// local setup against it. It holds the scan lock, so no scan that read the
// old baseline lands after it.
func (s *Service) setBaseline(ctx context.Context, b setupBaseline) error {
	if err := s.lockScan(ctx); err != nil {
		return err
	}
	defer s.unlockScan()
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	path := baselinePath(s.deps.StateDir)
	if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
		return err
	}
	if err := atomicWrite(path, append(data, '\n'), privateFile, os.Rename); err != nil {
		return err
	}
	sums := make(map[string]string, len(b.Entries))
	for _, e := range b.Entries {
		sums[e.Path] = e.SHA256
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last != nil {
		s.last.Drift = append(editedOnly(s.last.Drift), newLocalDrift(s.last.Entries, sums)...)
	}
	return nil
}

// KeepDrift makes what the managed folder now holds for an item its next
// version, when that content still hashes to sum, the drift the developer
// reviewed. It asks for no confirmation, so the version's actor is api and
// an item whose changes need the desktop app's confirmation
// (domain.LadoSetupNeedsPresence) is refused: the change flow keeps those
// with a proof. Content that is the latest version already keeps nothing
// and returns that version.
func (s *Service) KeepDrift(ctx context.Context, itemID domain.LadoSetupItemID, sum string) (domain.LadoSetupItemVersion, error) {
	return s.keepDrift(ctx, itemID, sum, approval{actor: actorAPI})
}

// keepDrift keeps an item's drift as KeepDrift does, as the approval's actor
// and with its presence proof, which an item that needs presence must have.
func (s *Service) keepDrift(ctx context.Context, itemID domain.LadoSetupItemID, sum string, ap approval) (domain.LadoSetupItemVersion, error) {
	if err := s.available(); err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	if err := s.lockWrite(ctx); err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	defer s.unlockWrite()
	iv, err := s.liveItem(ctx, itemID)
	if err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	it, latest := iv.Item, iv.Version
	if domain.LadoSetupNeedsPresence(it.Kind, it.Name) && ap.proof.Nonce == "" {
		return domain.LadoSetupItemVersion{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Keep this change in the AO desktop app. It can run commands or widen access, so the app asks you to confirm it.")
	}
	content, err := s.readManaged(ctx, it, latest)
	if err != nil {
		return domain.LadoSetupItemVersion{}, apierr.Conflict(CodeDriftUnreadable,
			"LADO cannot read the managed copy of this item as its content. Revert it to the latest version instead.",
			map[string]any{"item": string(it.ID), "error": plainError(err)})
	}
	v := content.version
	if v.SHA256 != sum {
		return domain.LadoSetupItemVersion{}, apierr.Conflict(CodeChanged, "The managed copy changed since you reviewed it. Review it again.",
			map[string]any{"item": string(it.ID), "sha256": v.SHA256})
	}
	if v.SHA256 == latest.SHA256 && v.ContentKind == latest.ContentKind && v.Executable == latest.Executable {
		return latest, nil
	}
	if err := checkKeepable(it, content); err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	for _, data := range content.blobs {
		if _, err := s.deps.Blobs.Put(data); err != nil {
			return domain.LadoSetupItemVersion{}, fmt.Errorf("store the managed copy of %s: %w", it.Name, err)
		}
	}
	now := s.now()
	v.ID, v.ItemID, v.Actor, v.CreatedAt = domain.LadoSetupItemVersionID(s.deps.NewID()), it.ID, ap.actor, now
	payload, _ := json.Marshal(map[string]any{
		"version": latest.Version + 1, "sha256": v.SHA256, "previousSha256": latest.SHA256,
		"proof": ap.proof.Nonce, "dev": ap.proof.Dev,
	})
	event := domain.LadoAuditEvent{At: now, Actor: ap.actor, Action: actionDriftKept, SubjectKind: subjectItem, SubjectID: string(it.ID), Payload: payload}
	var proof *domain.LadoPresenceProof
	if ap.proof.Nonce != "" {
		p := ap.proof
		proof = &p
	}
	err = s.deps.Store.AddLadoSetupItemVersion(ctx, it.ID, latest.Version, v, event, proof)
	switch {
	case errors.Is(err, domain.ErrLadoSetupItemChanged):
		return domain.LadoSetupItemVersion{}, apierr.Conflict(CodeItemChanged, "The item got another version meanwhile. Review it again.", map[string]any{"item": string(it.ID)})
	case errors.Is(err, domain.ErrLadoSetupItemNotLive):
		return domain.LadoSetupItemVersion{}, itemNotFound()
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return domain.LadoSetupItemVersion{}, ladopresence.Replayed()
	case err != nil:
		return domain.LadoSetupItemVersion{}, fmt.Errorf("keep the drift of %s: %w", it.Name, err)
	}
	v.Version = latest.Version + 1
	s.rescan(ctx)
	return v, nil
}

// RevertDrift writes an item's latest version over its managed copy. It
// needs no confirmation: the version had what it needed when it was made. A
// copy that holds the version is left as it is.
func (s *Service) RevertDrift(ctx context.Context, itemID domain.LadoSetupItemID) (domain.LadoSetupItemVersion, error) {
	if err := s.available(); err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	if err := s.lockWrite(ctx); err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	defer s.unlockWrite()
	iv, err := s.liveItem(ctx, itemID)
	if err != nil {
		return domain.LadoSetupItemVersion{}, err
	}
	it := iv.Item
	before, err := s.managedSum(ctx, it)
	if err == nil && s.compareManaged(iv, before) == nil {
		return iv.Version, nil
	}
	if err := s.writeManaged(iv); err != nil {
		if !isKeyKind(it.Kind) {
			return domain.LadoSetupItemVersion{}, fmt.Errorf("revert %s: %w", it.Name, err)
		}
		// The managed key file no longer reads as one: it is written again
		// from the versions of the items it holds.
		s.deps.Log.Warn("LADO writes a managed setup file again from its items", "item", it.Name, "err", err)
		if err := s.rewriteManagedKeyFile(ctx, it.Tool, it.Kind); err != nil {
			return domain.LadoSetupItemVersion{}, fmt.Errorf("revert %s: %w", it.Name, err)
		}
	}
	if err := s.checkManaged(ctx, iv); err != nil {
		return domain.LadoSetupItemVersion{}, fmt.Errorf("revert %s: %w", it.Name, err)
	}
	payload, _ := json.Marshal(map[string]any{"version": iv.Version.Version, "sha256": iv.Version.SHA256, "previousSha256": before})
	event := domain.LadoAuditEvent{At: s.now(), Actor: actorAPI, Action: actionDriftReverted, SubjectKind: subjectItem, SubjectID: string(it.ID), Payload: payload}
	if _, err := s.deps.Store.AppendLadoAuditEvent(ctx, event); err != nil {
		return domain.LadoSetupItemVersion{}, fmt.Errorf("record the revert of %s: %w", it.Name, err)
	}
	s.rescan(ctx)
	return iv.Version, nil
}

// liveItem returns a live item with its latest version.
func (s *Service) liveItem(ctx context.Context, id domain.LadoSetupItemID) (domain.LadoSetupItemWithVersion, error) {
	it, versions, ok, err := s.deps.Store.LadoSetupItem(ctx, id)
	if err != nil {
		return domain.LadoSetupItemWithVersion{}, fmt.Errorf("read the setup item: %w", err)
	}
	if !ok || !it.Live() || len(versions) == 0 {
		return domain.LadoSetupItemWithVersion{}, itemNotFound()
	}
	return domain.LadoSetupItemWithVersion{Item: it, Version: versions[len(versions)-1]}, nil
}

func itemNotFound() error {
	return apierr.NotFound(CodeNotFound, "LADO does not manage this setup item.")
}

// managedContent is an item's content in the managed folder as a version
// holds it, with every blob that version needs: the file, the JSON value,
// or each file of a folder and its tree.
type managedContent struct {
	version domain.LadoSetupItemVersion
	blobs   [][]byte
}

// readManaged reads an item's managed copy as managedSum hashes it: a key's
// canonical JSON, or what is at the item's path, a file or a folder. A file
// keeps the latest version's executable bit where the file system has none.
func (s *Service) readManaged(ctx context.Context, it domain.LadoSetupItem, latest domain.LadoSetupItemVersion) (managedContent, error) {
	if isKeyKind(it.Kind) {
		data, err := readRegularNoLink(managedKeyFile(s.deps.StateDir, it.Tool, it.Kind))
		if err != nil {
			return managedContent{}, err
		}
		value, err := keyValue(it.Tool, it.Kind, it.Name, data)
		if err != nil {
			return managedContent{}, err
		}
		return managedContent{
			version: domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentJSON, SHA256: sha256Hex(value), Size: int64(len(value))},
			blobs:   [][]byte{value},
		}, nil
	}
	path := managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name)
	info, err := os.Lstat(path)
	if err != nil {
		return managedContent{}, err
	}
	if !info.IsDir() {
		data, err := readRegularNoLink(path)
		if err != nil {
			return managedContent{}, err
		}
		exec := info.Mode().Perm()&0o111 != 0
		if runtime.GOOS == "windows" {
			exec = latest.Executable
		}
		return managedContent{
			version: domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentFile, SHA256: sha256Hex(data), Size: int64(len(data)), Executable: exec},
			blobs:   [][]byte{data},
		}, nil
	}
	tree, err := treeOf(ctx, path)
	if err != nil {
		return managedContent{}, err
	}
	manifest, err := tree.CanonicalJSON()
	if err != nil {
		return managedContent{}, err
	}
	c := managedContent{
		version: domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentTree, SHA256: sha256Hex(manifest), Size: tree.Size()},
	}
	for _, e := range tree.Entries {
		data, err := readRegularNoLink(filepath.Join(path, filepath.FromSlash(e.Path)))
		if err != nil {
			return managedContent{}, err
		}
		if sha256Hex(data) != e.SHA256 {
			return managedContent{}, fmt.Errorf("%s changed while LADO read it", e.Path)
		}
		c.blobs = append(c.blobs, data)
	}
	c.blobs = append(c.blobs, manifest)
	return c, nil
}

// checkKeepable refuses managed content that LADO would not adopt: a Codex
// MCP server must still go on a command line safely (plan decision d).
func checkKeepable(it domain.LadoSetupItem, c managedContent) error {
	if it.Tool != domain.LadoSetupToolCodex || it.Kind != domain.LadoSetupKindMCPServer {
		return nil
	}
	refuse := func(reason domain.LadoSetupReason) error {
		return apierr.Invalid(CodeNotAdoptable, "LADO cannot keep this change: the MCP server could not go on a Codex command line safely.", map[string]any{
			"tool": string(it.Tool), "kind": string(it.Kind), "name": it.Name, "reason": string(reason),
		})
	}
	table, err := codexServerTable(it.Name, c.blobs[0])
	if err != nil {
		return refuse(domain.LadoSetupReasonUnreadable)
	}
	if literalCredentials(table) {
		return refuse(domain.LadoSetupReasonLiteralCredentials)
	}
	return nil
}

// rewriteManagedKeyFile writes the managed key file of a tool and kind from
// scratch with the latest versions of the live items it holds.
func (s *Service) rewriteManagedKeyFile(ctx context.Context, tool domain.LadoSetupTool, kind domain.LadoSetupKind) error {
	items, err := s.deps.Store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		return fmt.Errorf("list the managed setup items: %w", err)
	}
	var out []byte
	for _, iv := range items {
		if iv.Item.Tool != tool || iv.Item.Kind != kind {
			continue
		}
		content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return fmt.Errorf("read %s from its blob: %w", iv.Item.Name, err)
		}
		if out, err = withKey(iv.Item, out, content); err != nil {
			return err
		}
	}
	path := managedKeyFile(s.deps.StateDir, tool, kind)
	if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
		return err
	}
	return atomicWrite(path, out, privateFile, os.Rename)
}

// rescan renews the cached inventory after LADO wrote the managed folder or
// an original location. A failure is logged; the next scan catches up.
func (s *Service) rescan(ctx context.Context) {
	if _, err := s.Scan(ctx); err != nil {
		s.deps.Log.Warn("LADO could not scan the local setup again", "err", err)
	}
}
