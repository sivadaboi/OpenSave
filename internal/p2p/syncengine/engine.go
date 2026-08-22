package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// Conflict is a diverged-save state awaiting user resolution.
type Conflict struct {
	Peer       Peer         `json:"peer"`
	LocalSnap  SnapshotInfo `json:"localSnap"`
	RemoteSnap SnapshotInfo `json:"remoteSnap"`
	// Comparison data so the user can make an informed choice.
	LocalStats  SideStats  `json:"localStats"`
	RemoteStats SideStats  `json:"remoteStats"`
	DiffFiles   []DiffFile `json:"diffFiles"` // capped; DiffTotal is the real count
	DiffTotal   int        `json:"diffTotal"`
}

// SideStats summarises one side's save state for the conflict UI.
type SideStats struct {
	Files         int   `json:"files"`
	TotalBytes    int64 `json:"totalBytes"`
	LatestMtimeMs int64 `json:"latestMtimeMs"`
}

// DiffFile is one path that differs between the two sides. Sizes are -1
// when the file doesn't exist on that side.
type DiffFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // changed | only-local | only-remote
	LocalSize  int64  `json:"localSize"`
	RemoteSize int64  `json:"remoteSize"`
}

// ErrSyncQueued means a sync was already running for this game, so the
// request was queued: a follow-up pass runs automatically when the active
// sync finishes. Callers should treat this as success-in-progress, not a
// failure.
var ErrSyncQueued = errors.New("a sync is already running for this game — your change is queued and will sync right after")

// perPeerSyncTimeout caps one game/peer sync pass. Generous (large saves
// on slow links) but finite — a hung transport must never wedge the
// engine. Var so tests can shrink it.
var perPeerSyncTimeout = 30 * time.Minute

// Result summarizes one game/peer sync run.
type Result struct {
	Status    string `json:"status"` // in_sync | updated | updated_bidirectional | deletions_synced | triggered_peer_pull | conflict
	Direction string `json:"direction"`
	PeerID    string `json:"peerId,omitempty"`
	PeerName  string `json:"peerName,omitempty"`
}

// Engine orchestrates sync runs. Construct with New.
type Engine struct {
	Store     *store.Store
	Snapshots *snapshot.Manager
	Transport Transport
	Progress  ProgressCallbacks
	Log       func(level, msg string)

	// OnlinePeers resolves who is reachable right now. Only the queued
	// follow-up uses it: every other caller passes the list it just looked
	// up. Optional — without it the follow-up falls back to the list the
	// sync it queued behind was started with.
	OnlinePeers func() []Peer

	mu              sync.Mutex
	activeSyncs     map[string]bool
	pendingSyncs    map[string]bool // a sync was requested while one ran
	activeConflicts map[string]*Conflict
	// rootConflicts holds divergences in a game's EXTRA save locations, keyed
	// by game and location so several can wait on a decision at once.
	rootConflicts map[string]*RootConflict
}

// New creates an Engine.
func New(s *store.Store, snaps *snapshot.Manager, transport Transport) *Engine {
	return &Engine{
		Store:           s,
		Snapshots:       snaps,
		Transport:       transport,
		Log:             func(string, string) {},
		activeSyncs:     map[string]bool{},
		pendingSyncs:    map[string]bool{},
		activeConflicts: map[string]*Conflict{},
		rootConflicts:   map[string]*RootConflict{},
	}
}

// ActiveConflicts returns a snapshot of unresolved conflicts by game id.
func (e *Engine) ActiveConflicts() map[string]Conflict {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Conflict, len(e.activeConflicts))
	for id, c := range e.activeConflicts {
		out[id] = *c
	}
	return out
}

// SyncGame syncs one game with every online paired peer. A concurrent
// call for the same game doesn't run twice — but it must not be LOST
// either: the in-flight sync captured its manifest before the new change
// existed, so dropping the request silently loses that change until the
// periodic reconcile. Instead, the request is queued and one follow-up
// pass runs when the active sync finishes.
// isGameNotFound reports whether a manifest-fetch error means the peer
// isn't tracking this game (as opposed to a network/transport failure).
// The serving side answers "Game not found." for both an unknown game and
// one it has tombstoned after untracking.
func isGameNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

func (e *Engine) SyncGame(ctx context.Context, gameID string, onlinePeers []Peer) (map[string]Result, error) {
	e.mu.Lock()
	if e.activeSyncs[gameID] {
		e.pendingSyncs[gameID] = true
		e.mu.Unlock()
		return nil, ErrSyncQueued
	}
	e.activeSyncs[gameID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.activeSyncs, gameID)
		rerun := e.pendingSyncs[gameID]
		delete(e.pendingSyncs, gameID)
		e.mu.Unlock()
		if rerun {
			e.Log("info", fmt.Sprintf("running queued follow-up sync for %s", gameID))
			// Fresh context: the queued requester's may already be gone.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()

				// Resolve peers now rather than reusing the list the earlier
				// sync started with. That list describes the moment this
				// follow-up was queued behind, which can be minutes old: a
				// device may have dropped, come back on a different address,
				// or moved between LAN and relay. Syncing a queued change to
				// the wrong address fails in a goroutine nobody is watching,
				// and the caller was told "queued and will sync right after".
				peers := onlinePeers
				if e.OnlinePeers != nil {
					peers = e.OnlinePeers()
				}
				if len(peers) == 0 {
					// Say so. Returning quietly here is indistinguishable from
					// a completed sync, and the change simply never left.
					e.Log("warn", fmt.Sprintf(
						"queued follow-up sync for %s found no reachable devices — "+
							"it will go out on the next sync", gameID))
					return
				}
				if _, err := e.SyncGame(ctx, gameID, peers); err != nil {
					e.Log("warn", fmt.Sprintf("queued follow-up sync for %s: %v", gameID, err))
				}
			}()
		}
	}()

	results := map[string]Result{}
	for _, peer := range onlinePeers {
		// Hard per-peer cap: a wedged transport must never hold
		// activeSyncs forever (which would silently block every future
		// sync of this game until an app restart).
		peerCtx, cancel := context.WithTimeout(ctx, perPeerSyncTimeout)
		res, err := e.SyncWithPeer(peerCtx, gameID, peer)
		cancel()
		if err != nil {
			e.Log("error", fmt.Sprintf("sync %s with %s failed: %v", gameID, peer.Name, err))
			results[peer.ID] = Result{Status: "error", PeerID: peer.ID, PeerName: peer.Name}
			continue
		}
		results[peer.ID] = res
		// Only a genuinely completed sync advances the "last synced" baseline.
		// Advancing it on a conflict (or error) would hide the still-unresolved
		// divergence from the NEXT sync, causing the peer to silently overwrite
		// its own changes instead of detecting the conflict and asking.
		if res.Status != "conflict" && res.Status != "error" {
			_ = e.Store.UpdatePeerLastSynced(peer.ID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
		}
	}
	return results, nil
}

// SyncWithPeer runs the full state machine against a single peer.
func (e *Engine) SyncWithPeer(ctx context.Context, gameID string, peer Peer) (Result, error) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return Result{}, err
	}
	e.Log("info", fmt.Sprintf("syncing %q with %q (%s)", game.Name, peer.Name,
		map[bool]string{true: "WAN relay", false: "direct LAN"}[peer.Wan()]))

	// 1. Fetch remote manifest + branch info.
	isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath)
	remoteData, err := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{
		Name: game.Name, SavePath: game.SavePath, IsFile: isFile,
		AppID: game.AppID, CoverURL: game.CoverURL,
	})
	if err != nil {
		// The peer simply isn't tracking this game (they untracked it, or
		// never had it). That's a stable state, not a transient network
		// interruption — surface it as "peer_missing" so the resync loop
		// stops hammering it every tick instead of retrying forever.
		if isGameNotFound(err) {
			return Result{Status: "peer_missing", PeerID: peer.ID, PeerName: peer.Name}, nil
		}
		return Result{}, fmt.Errorf("fetch remote manifest: %w", err)
	}

	// 2. Branch alignment: local follows the remote's active branch.
	if remoteData.ActiveBranch != "" && game.ActiveBranch != remoteData.ActiveBranch {
		e.Log("warn", fmt.Sprintf("branch mismatch on %q: local %q vs remote %q — switching local",
			game.Name, game.ActiveBranch, remoteData.ActiveBranch))
		// Not seeded: this branch is being created to receive the peer's
		// state, which the rest of this sync pulls in. The local save is
		// preserved by the safety snapshot the switch takes.
		if _, err := e.Snapshots.CreateBranch(gameID, remoteData.ActiveBranch, false); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			return Result{}, err
		}
		if err := e.Snapshots.SwitchBranch(gameID, remoteData.ActiveBranch); err != nil {
			return Result{}, err
		}
		game, err = e.Store.GetGame(gameID)
		if err != nil {
			return Result{}, err
		}
	}

	localManifest, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return Result{}, fmt.Errorf("build local manifest: %w", err)
	}

	// Apply this game's exclusion rules to BOTH sides, before anything is
	// compared, hashed or decided. From here on an excluded path simply does
	// not exist: it cannot be pulled, pushed, deleted, or counted into a merge
	// base. See ignore.go for why filtering the manifest at build time instead
	// would propagate a deletion of the very file being protected.
	ignoreRules := e.rulesFor(gameID)
	unfilteredLocal, unfilteredRemote := localManifest, remoteData.Manifest
	if !ignoreRules.Empty() {
		localManifest = filterManifest(localManifest, ignoreRules)
		remoteData.Manifest = filterManifest(remoteData.Manifest, ignoreRules)
	}

	// 3. Existing unresolved conflict blocks further syncing.
	e.mu.Lock()
	if existing := e.activeConflicts[gameID]; existing != nil {
		e.mu.Unlock()
		return Result{Status: "conflict", PeerID: peer.ID, PeerName: peer.Name}, nil
	}
	e.mu.Unlock()

	// 4. Conflict detection (lineage + skew-tolerant mtimes).
	lastSyncMs := e.lastSyncTimeMs(peer.ID)
	agreedHash := e.Store.GetAgreedHash(gameID, peer.ID)

	// Self-heal a stale merge-base before judging anything against it.
	//
	// After a push this side deliberately leaves the base behind (see the
	// ratchet at the end of this function): the peer is the one that ends up
	// holding the new state, so convergence is recorded when it reports back
	// via sync-complete. That report travels the network, and if it is lost
	// the base stays frozen at a state neither side holds any more.
	//
	// A frozen base does not merely delay a conflict, it manufactures one.
	// DetectConflict asks whether BOTH sides moved off the base; once the
	// base is behind both of them, the answer is yes forever — so the next
	// ordinary one-sided edit reads as a two-way divergence and prompts on a
	// save the peer never touched.
	//
	// When the two sides hold the same files, that is a convergence provable
	// right here from data we already have, without waiting to be told. Bank
	// it. Directory-only differences count: they are not disagreements about
	// save content, and the sync creates the missing folder anyway.
	if agreedHash != "" && agreedHash != localManifest.ManifestHash() &&
		sameFiles(localManifest, remoteData.Manifest) {
		_ = e.Store.SetAgreedHash(gameID, peer.ID, localManifest.ManifestHash())
		agreedHash = localManifest.ManifestHash()
	}
	// The same repair for the case the one above cannot reach: the sides no
	// longer hold the same files, because this device carried on and changed
	// something after the push. That is the ordinary case — the user played
	// the game — and it is precisely when a stranded base bites, because
	// sameFiles is false and nothing else ever advances it.
	//
	// The push itself is still provable though. If the peer is holding
	// exactly the state we handed it, it applied that push, whatever became
	// of its report. Both sides verifiably held that hash, which is the
	// definition of a merge-base, so bank it and judge the divergence from
	// there instead of from a state neither side has held for hours.
	//
	// The record is cleared the moment any convergence is recorded (see
	// SetAgreedHash), so a non-empty one here means a push really is still
	// outstanding rather than merely having happened at some point. Without
	// that, a record left over from an earlier push would match again if the
	// peer ever returned to that state, dragging the base backwards onto it
	// and skipping a conflict that a later agreement had earned.
	if pushed := e.Store.GetPushedHash(gameID, peer.ID); pushed != "" {
		if remoteHash := remoteData.Manifest.ManifestHash(); agreedHash != remoteHash && remoteHash == pushed {
			_ = e.Store.SetAgreedHash(gameID, peer.ID, remoteHash)
			agreedHash = remoteHash
		}
	}

	// A merge base recorded before the exclusion rules existed was hashed over
	// the whole save, so it cannot equal either side's filtered hash — and a
	// base that matches neither side reads as both having moved: a conflict on
	// the first sync after anyone adds a rule.
	//
	// Rewriting the base when the rules change is not enough on its own,
	// because an in-flight sync can record an unfiltered one straight
	// afterwards. Translating it here needs no such timing to hold: if a
	// side's UNFILTERED state still hashes to the base then that side has not
	// changed since, whichever view the base was written in, and its filtered
	// hash is the same fact expressed in today's terms.
	if !ignoreRules.Empty() && agreedHash != "" {
		switch agreedHash {
		case unfilteredLocal.ManifestHash():
			agreedHash = localManifest.ManifestHash()
		case unfilteredRemote.ManifestHash():
			agreedHash = remoteData.Manifest.ManifestHash()
		}
	}

	if DetectConflict(localManifest, remoteData.Manifest, lastSyncMs, agreedHash) {
		e.registerConflict(gameID, peer, localManifest, remoteData)
		return Result{Status: "conflict", PeerID: peer.ID, PeerName: peer.Name}, nil
	}

	// 5. Classification.
	lineageFiles, lineageDirs, err := e.lineageSets(gameID, peer.ID)
	if err != nil {
		return Result{}, err
	}
	// The lineage is filtered too, and this is the half that is easy to
	// forget: on a game that synced BEFORE the rule was written, the excluded
	// path is still recorded as shared. Leave it there and the decision reads
	// "we both had this and now I do not" — and propagates a deletion of the
	// file the rule exists to protect.
	if rules := e.rulesFor(gameID); !rules.Empty() {
		lineageFiles = filterLineage(lineageFiles, rules)
		lineageDirs = filterLineage(lineageDirs, rules)
	}
	decision := Compute(localManifest, remoteData.Manifest, lineageFiles, lineageDirs)

	if !decision.HasChanges() {
		e.Log("success", fmt.Sprintf("%q already in sync with %q", game.Name, peer.Name))
		e.persistLineage(gameID, peer.ID, localManifest, remoteData.Manifest)
		// Both sides verifiably identical: this is a convergence point.
		_ = e.Store.SetAgreedHash(gameID, peer.ID, localManifest.ManifestHash())
		// The peer must record this state too: it took no part in this
		// exchange beyond serving its manifest, and without lineage or a
		// last-synced time on its side, its NEXT pull from us misreads its
		// own (identical) copy as a divergent change — a false conflict.
		// The event carries the verified hash so the peer can safely
		// re-confirm identity against its own current files (see
		// ConfirmInSync).
		e.Transport.ReportSyncEvent(peer, gameID, "in-sync", map[string]any{
			"peerName":     e.deviceName(),
			"manifestHash": localManifest.ManifestHash(),
		})
		// The primary location agreeing says nothing about the others.
		e.syncExtraRoots(ctx, gameID, game, peer, remoteData)
		return Result{Status: "in_sync", Direction: "none"}, nil
	}

	// Nothing below is about files arriving, only about local files leaving.
	// A pull that brings files this device never held destroys nothing.
	atRisk := filesAtRisk(localManifest, decision)

	// Take the copy before doing the damage, rather than reasoning about
	// whether the damage is survivable.
	//
	// The check underneath used to stand alone: it asked whether the save held
	// anything no snapshot captured and, if so, refused to sync and raised a
	// conflict. That protects the files but it answers a whole-save question
	// about a per-file danger — one edit anywhere blocked every sync for the
	// game — and it leans on a record that only the watcher's automatic
	// snapshot ever wrote, so it was as likely to be stale as accurate.
	//
	// A snapshot here removes the question. Whatever these files hold now is
	// recoverable from this moment on, so the sync can proceed on its merits.
	//
	// And if it cannot be taken, the sync stops. There used to be a fallback
	// that consulted Game.LastManifestHash to guess whether overwriting
	// unprotected files was survivable, which is the wrong shape of answer
	// twice over: that value is maintained by other subsystems and goes stale
	// without anyone noticing, and guessing is not what to do when the honest
	// statement is "your save could not be backed up". Refusing is visible and
	// fixable — a full disk, a missing backups folder — where overwriting a
	// save with no copy behind it is neither.
	if len(atRisk) > 0 {
		if _, err := e.Snapshots.Create(gameID, "before sync replaced local files", true); err != nil {
			e.Log("error", fmt.Sprintf(
				"not syncing %q with %q: %d local file(s) would be replaced and they could not be snapshotted first: %v",
				game.Name, peer.Name, len(atRisk), err))
			return Result{}, fmt.Errorf(
				"refusing to replace %d local file(s) for %q: they could not be snapshotted first: %w",
				len(atRisk), game.Name, err)
		}
	}

	// 6. Apply deletions (locally + propagate to peer).
	e.applyLocalDeletions(primaryRootOf(game), decision)
	e.propagateDeletions(ctx, peer, gameID, primaryRootOf(game), decision)

	// 7. Create pulled directories (parents first).
	e.createPulledDirs(game, decision.DirsToPull)

	// 8. Pull changed files.
	if len(decision.FilesToPull) > 0 {
		if err := e.pullFiles(ctx, peer, gameID, game, primaryRootOf(game), localManifest, remoteData, decision.FilesToPull); err != nil {
			return Result{}, err
		}
	}

	// 9. Trigger a reciprocal pull when we hold newer content.
	//
	// handedOver is the state the peer is about to take, captured BEFORE it is
	// told to pull. It becomes the push record, and it has to be read here
	// rather than after the sync: the whole point of that record is to be a
	// hash the peer can later be observed holding, and a save re-read at the
	// end of the sync has already moved on for any game that writes while it
	// is running — which is the case the record exists for. A hash this device
	// reached after the peer had pulled names a state nobody else ever held,
	// so the repair that looks for it can never fire.
	//
	// It costs one extra walk of the save folder, on push syncs only. Reading
	// it is the only way to be accurate: local files changed in steps 6-8, so
	// neither the decision-time manifest nor the post-sync one describes what
	// the peer is being offered.
	//
	// Still an approximation — the peer pulls asynchronously and could take a
	// newer state than this. That direction is safe: a push record that no
	// longer matches simply fails to redeem the base, whereas the repair only
	// ever acts on an exact match with what the peer reports holding.
	var handedOver string
	if decision.HasPush() {
		if m, err := delta.BuildManifest(game.SavePath); err == nil {
			handedOver = m.ManifestHash()
		}
		e.Log("info", fmt.Sprintf("local has newer content; triggering %q to pull", peer.Name))
		e.Transport.TriggerPeerPull(peer, gameID)
	}

	// 10. Record the post-sync lineage. The fresh manifest captures files
	// we just pulled — but it must be MERGED with the decision-time
	// manifest: a file deleted locally while this sync ran was still
	// verifiably on both sides, and dropping it from the lineage here
	// would make the next pass misread the peer's copy as a brand-new
	// remote file and resurrect it, instead of propagating the deletion.
	freshManifest, freshErr := delta.BuildManifest(game.SavePath)
	if freshErr == nil {
		e.persistLineage(gameID, peer.ID, mergeManifestPaths(freshManifest, localManifest), remoteData.Manifest)
	}

	// Convergence ratchet: after a pure pull (no push, no peer-side
	// deletions) we now hold exactly the remote's state — record it as the
	// agreed merge-base.
	//
	// Pushes and peer-deletions cannot ratchet here, because this side does
	// not yet know the peer applied anything; they converge when the peer
	// reports back (sync-complete → RefreshLineage) or the next in_sync pass
	// confirms. That was long assumed harmless on the grounds that a stale
	// base only ever errs toward an extra conflict prompt rather than toward
	// overwriting anything. Safe, but not harmless: if the report is lost the
	// base never advances at all, and a base stranded behind both sides turns
	// every subsequent one-sided edit into a false conflict. So record what
	// was handed over, and let the next sync prove the push landed by
	// observing the peer holding exactly it.
	switch {
	case !decision.HasPush() &&
		len(decision.FilesToDeleteOnPeer) == 0 && len(decision.DirsToDeleteOnPeer) == 0:
		_ = e.Store.SetAgreedHash(gameID, peer.ID, remoteData.Manifest.ManifestHash())
	case handedOver != "":
		// What the peer was actually offered, from step 9.
		_ = e.Store.SetPushedHash(gameID, peer.ID, handedOver)
	case freshErr == nil:
		// Peer-side deletions with no push: nothing was handed over, so the
		// state after this sync is the best description of where the peer is
		// being asked to end up.
		_ = e.Store.SetPushedHash(gameID, peer.ID, freshManifest.ManifestHash())
	}

	// Extra locations sync last and on their own terms, so the primary save —
	// the thing anyone actually opened the app about — is already settled and
	// recorded before any of them is touched.
	e.syncExtraRoots(ctx, gameID, game, peer, remoteData)

	return e.classifyResult(decision), nil
}

func (e *Engine) classifyResult(d Decision) Result {
	switch {
	case d.HasPull() && d.HasPush():
		return Result{Status: "updated_bidirectional", Direction: "bidirectional"}
	case d.HasPull():
		return Result{Status: "updated", Direction: "pull"}
	case d.HasDeletions() && !d.HasPush():
		return Result{Status: "deletions_synced", Direction: "none"}
	default:
		return Result{Status: "triggered_peer_pull", Direction: "push"}
	}
}

func (e *Engine) lastSyncTimeMs(peerID string) int64 {
	peer, err := e.Store.GetPeer(peerID)
	if err != nil || !peer.LastSynced.Valid || peer.LastSynced.String == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", peer.LastSynced.String)
	if err != nil {
		if t2, err2 := time.Parse(time.RFC3339, peer.LastSynced.String); err2 == nil {
			return t2.UnixMilli()
		}
		return 0
	}
	return t.UnixMilli()
}

func (e *Engine) lineageSets(gameID, peerID string) (files, dirs map[string]struct{}, err error) {
	fileList, dirList, err := e.Store.GetSyncState(gameID, peerID)
	if err != nil {
		return nil, nil, err
	}
	return toSet(fileList), toSet(dirList), nil
}

// persistLineage records the paths BOTH sides verifiably had at the end of
// a successful sync — strictly the intersection of the two manifests.
//
// Recording local-only paths (as this once did) is how a user's file got
// deleted: after a push-trigger sync we recorded "the peer has this file"
// before the peer had actually pulled it. The peer's pull kept failing (AV
// lock on a fresh .exe), so on the next run "in lineage but missing on
// peer" was misread as "the peer deleted it" — and the local original was
// removed. Unconfirmed pushes must never enter the lineage; they join it
// on the first sync after the peer's manifest actually contains them.
func (e *Engine) persistLineage(gameID, peerID string, local, remote delta.Manifest) {
	files, dirs := IntersectLineage(local, remote)
	// Excluded paths must never re-enter the record of what both sides hold.
	// RefreshLineage rebuilds this from unfiltered manifests, so without a
	// filter here an excluded file would be written back in — and the next
	// sync would read it as shared, then as deleted, and propagate that.
	if rules := e.rulesFor(gameID); !rules.Empty() {
		files = filterPathList(files, rules)
		dirs = filterPathList(dirs, rules)
	}
	if err := e.Store.SetSyncState(gameID, peerID, files, dirs); err != nil {
		e.Log("warn", fmt.Sprintf("persist sync lineage failed: %v", err))
	}
}

// mergeManifestPaths unions the path sets of two manifests (entries from a
// win on collision). Used to keep decision-time paths alive in the lineage
// even when they disappeared locally while the sync ran.
func mergeManifestPaths(a, b delta.Manifest) delta.Manifest {
	merged := delta.Manifest{Files: make(map[string]delta.FileEntry, len(a.Files)+len(b.Files))}
	for p, fe := range b.Files {
		merged.Files[p] = fe
	}
	for p, fe := range a.Files {
		merged.Files[p] = fe
	}
	seen := map[string]bool{}
	for _, d := range append(append([]string{}, a.Dirs...), b.Dirs...) {
		if !seen[d] {
			seen[d] = true
			merged.Dirs = append(merged.Dirs, d)
		}
	}
	return merged
}

// IntersectLineage returns the sorted file and dir paths present in both
// manifests — the only paths that may count as "synced on both sides".
func IntersectLineage(local, remote delta.Manifest) (files, dirs []string) {
	files = make([]string, 0, len(local.Files))
	for p := range local.Files {
		if _, ok := remote.Files[p]; ok {
			files = append(files, p)
		}
	}
	sort.Strings(files)

	remoteDirs := toSet(remote.Dirs)
	dirs = make([]string, 0, len(local.Dirs))
	for _, d := range local.Dirs {
		if _, ok := remoteDirs[d]; ok {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return files, dirs
}

// ConfirmInSync handles a peer's "in-sync" report: the peer verified both
// sides held identical content (claimedHash). If OUR current files still
// hash to that value, identity is re-proven right now on our own clock —
// so recording the lineage and last-synced time is safe (no clock-skew or
// stale-timestamp risk). If anything changed in the window, fall back to a
// plain lineage refresh and record no timestamp: the conflict guard's
// window must never shrink on unverified state.
func (e *Engine) ConfirmInSync(ctx context.Context, gameID string, peer Peer, claimedHash string) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return
	}
	local, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return
	}
	if claimedHash != "" && local.ManifestHash() == claimedHash {
		e.persistLineage(gameID, peer.ID, local, local) // identical sides: lineage = our own paths
		_ = e.Store.SetAgreedHash(gameID, peer.ID, claimedHash)
		_ = e.Store.UpdatePeerLastSynced(peer.ID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
		return
	}
	e.RefreshLineage(ctx, gameID, peer)
}

// RefreshLineage re-fetches the peer's manifest and re-persists the shared
// lineage. Called when a peer reports it finished pulling from us: the
// files we pushed are now really on both sides, so they can safely enter
// the lineage (making future local deletions of them propagate instead of
// the file being pulled back).
func (e *Engine) RefreshLineage(ctx context.Context, gameID string, peer Peer) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return
	}
	isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath)
	remoteData, err := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{
		Name: game.Name, SavePath: game.SavePath, IsFile: isFile,
		AppID: game.AppID, CoverURL: game.CoverURL,
	})
	if err != nil {
		return
	}
	local, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return
	}
	e.persistLineage(gameID, peer.ID, local, remoteData.Manifest)
	// Peer finished pulling: if both sides now hash identically, that's a
	// verified convergence — ratchet the merge-base.
	if local.ManifestHash() == remoteData.Manifest.ManifestHash() {
		_ = e.Store.SetAgreedHash(gameID, peer.ID, local.ManifestHash())
	}
	e.refreshRootLineage(gameID, remoteData, peer)
}

// refreshRootLineage does the same for a game's extra save locations.
//
// Without it those locations only ever get lineage on the side that STARTED a
// sync. The receiving side stays blank, and a blank lineage cannot tell "the
// other device deleted this file" from "the other device has never had it" —
// so it reads a deletion as a file it ought to push, and sends it straight
// back. The deletion undoes itself, on the very device that made it.
//
// That went unnoticed until extra locations became watched: before that,
// nothing ever prompted the receiving side to start a sync of one, so its
// empty lineage was never consulted.
func (e *Engine) refreshRootLineage(gameID string, remoteData ManifestResponse, peer Peer) {
	for _, sr := range e.sharedRoots(gameID, remoteData) {
		local, err := delta.BuildManifest(sr.root.Path)
		if err != nil {
			continue
		}
		e.persistRootLineage(gameID, peer.ID, sr.root.Name, local, sr.remote)
		if local.RootHash(delta.PrimaryRoot) == sr.remote.RootHash(delta.PrimaryRoot) {
			_ = e.Store.SetAgreedHashForRoot(gameID, peer.ID, sr.root.Name,
				local.RootHash(delta.PrimaryRoot))
		}
	}
}

func (e *Engine) registerConflict(gameID string, peer Peer, localManifest delta.Manifest, remoteData ManifestResponse) {
	localSnap := SnapshotInfo{ID: "current", Timestamp: time.UnixMilli(int64(localManifest.LatestMtime)).UTC().Format(time.RFC3339), Comment: "Current active saves"}
	if latest, err := e.Snapshots.LatestSnapshot(gameID, ""); err == nil {
		localSnap = SnapshotInfo{ID: latest.ID, Timestamp: latest.Timestamp, Comment: latest.Comment}
	}
	remoteSnap := SnapshotInfo{ID: "remote-current", Timestamp: time.UnixMilli(int64(remoteData.Manifest.LatestMtime)).UTC().Format(time.RFC3339), Comment: "Current peer saves"}
	if remoteData.LatestSnapshot != nil {
		remoteSnap = *remoteData.LatestSnapshot
	}

	// Capture comparison data while we hold both manifests, so the UI can
	// show which side is further along and exactly what differs.
	diffs := diffManifests(localManifest, remoteData.Manifest)
	const maxDiffFiles = 100
	total := len(diffs)
	if len(diffs) > maxDiffFiles {
		diffs = diffs[:maxDiffFiles]
	}

	e.mu.Lock()
	e.activeConflicts[gameID] = &Conflict{
		Peer: peer, LocalSnap: localSnap, RemoteSnap: remoteSnap,
		LocalStats:  manifestStats(localManifest),
		RemoteStats: manifestStats(remoteData.Manifest),
		DiffFiles:   diffs,
		DiffTotal:   total,
	}
	e.mu.Unlock()

	e.Log("warn", fmt.Sprintf("sync conflict on %q with %q: both sides modified since last sync", gameID, peer.Name))
	if e.Progress.OnConflict != nil {
		e.Progress.OnConflict(gameID)
	}
}

// manifestStats summarises a manifest for the conflict comparison UI.
func manifestStats(m delta.Manifest) SideStats {
	s := SideStats{Files: len(m.Files), LatestMtimeMs: int64(m.LatestMtime)}
	for _, f := range m.Files {
		s.TotalBytes += f.Size
	}
	return s
}

// diffManifests lists every path that differs between the two sides,
// sorted by path.
func diffManifests(local, remote delta.Manifest) []DiffFile {
	var out []DiffFile
	for p, lf := range local.Files {
		if rf, ok := remote.Files[p]; ok {
			if lf.Hash != rf.Hash {
				out = append(out, DiffFile{Path: p, Status: "changed", LocalSize: lf.Size, RemoteSize: rf.Size})
			}
		} else {
			out = append(out, DiffFile{Path: p, Status: "only-local", LocalSize: lf.Size, RemoteSize: -1})
		}
	}
	for p, rf := range remote.Files {
		if _, ok := local.Files[p]; !ok {
			out = append(out, DiffFile{Path: p, Status: "only-remote", LocalSize: -1, RemoteSize: rf.Size})
		}
	}

	// Directories count towards the manifest hash, so a conflict can involve
	// them; listing only files left the modal unable to account for part of
	// what it was asking about. Sizes stay -1 — a folder has none — which
	// the UI already renders as "—".
	localDirs := make(map[string]struct{}, len(local.Dirs))
	for _, d := range local.Dirs {
		localDirs[d] = struct{}{}
	}
	remoteDirs := make(map[string]struct{}, len(remote.Dirs))
	for _, d := range remote.Dirs {
		remoteDirs[d] = struct{}{}
	}
	for _, d := range local.Dirs {
		if _, ok := remoteDirs[d]; !ok {
			out = append(out, DiffFile{Path: d + "/", Status: "only-local", LocalSize: -1, RemoteSize: -1})
		}
	}
	for _, d := range remote.Dirs {
		if _, ok := localDirs[d]; !ok {
			out = append(out, DiffFile{Path: d + "/", Status: "only-remote", LocalSize: -1, RemoteSize: -1})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// syncRoot is one save location taking part in a sync: the name both devices
// know it by, and where it lives on this device. The two always travel
// together — a name with no local path cannot be written to, and a path with
// no name cannot be matched against what the peer sent.
type syncRoot struct {
	Name string // "" is the primary location, i.e. game.SavePath
	Path string
}

// primaryRootOf is the single-location view every existing game has.
func primaryRootOf(game store.Game) syncRoot {
	return syncRoot{Name: delta.PrimaryRoot, Path: game.SavePath}
}

func (e *Engine) applyLocalDeletions(root syncRoot, d Decision) {
	for _, relPath := range d.FilesToDeleteLocally {
		if !delta.IsSafePath(root.Path, relPath) {
			e.Log("warn", "path traversal deletion denied: "+relPath)
			continue
		}
		full := filepath.Join(root.Path, filepath.FromSlash(relPath))
		_ = os.Chmod(full, 0o666)
		if err := os.Remove(full); err == nil {
			e.Log("info", "deleted locally (peer deleted): "+relPath)
		}
	}

	// Deepest first so children go before parents.
	dirs := append([]string{}, d.DirsToDeleteLocally...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, relDir := range dirs {
		if !delta.IsSafePath(root.Path, relDir) {
			continue
		}
		full := filepath.Join(root.Path, filepath.FromSlash(relDir))
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			if err := os.Remove(full); err == nil { // only removes empty dirs, matching rmdirSync
				e.Log("info", "deleted directory locally (peer deleted): "+relDir)
			}
		}
	}
}

func (e *Engine) propagateDeletions(ctx context.Context, peer Peer, gameID string, root syncRoot, d Decision) {
	for _, relPath := range d.FilesToDeleteOnPeer {
		if !delta.IsSafePath(root.Path, relPath) {
			continue
		}
		ref := FileRef{GameID: gameID, Root: root.Name, RelPath: relPath}
		if err := e.Transport.DeleteRemote(ctx, peer, ref); err != nil {
			e.Log("warn", fmt.Sprintf("could not propagate deletion of %s: %v", relPath, err))
		}
	}
	dirs := append([]string{}, d.DirsToDeleteOnPeer...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, relDir := range dirs {
		if !delta.IsSafePath(root.Path, relDir) {
			continue
		}
		ref := FileRef{GameID: gameID, Root: root.Name, RelPath: relDir}
		if err := e.Transport.DeleteRemote(ctx, peer, ref); err != nil {
			e.Log("warn", fmt.Sprintf("could not propagate dir deletion of %s: %v", relDir, err))
		}
	}
}

func (e *Engine) createPulledDirs(game store.Game, dirsToPull []string) {
	e.createPulledDirsIn(primaryRootOf(game), dirsToPull)
}

func (e *Engine) createPulledDirsIn(root syncRoot, dirsToPull []string) {
	dirs := append([]string{}, dirsToPull...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) < len(dirs[j]) }) // parents first
	for _, relDir := range dirs {
		if !delta.IsSafePath(root.Path, relDir) {
			continue
		}
		_ = os.MkdirAll(filepath.Join(root.Path, filepath.FromSlash(relDir)), 0o777)
	}
}

// pullFiles downloads and patches every file in filesToPull with bounded
// concurrency, progress reporting, throttling, and a mirror snapshot at
// the end.
// pullFiles patches one save location. game is carried alongside root
// because snapshot mirroring is a game-level concern while every path
// decision belongs to the location — keeping them separate is what stops a
// second location's files being written relative to the primary save path.
func (e *Engine) pullFiles(ctx context.Context, peer Peer, gameID string, game store.Game, root syncRoot,
	localManifest delta.Manifest, remoteData ManifestResponse, filesToPull []string) (retErr error) {

	deviceName := e.deviceName()
	if e.Progress.OnSyncStart != nil {
		e.Progress.OnSyncStart(gameID, ProgressEvent{PeerName: peer.Name, Direction: "download"})
	}
	e.Transport.ReportSyncEvent(peer, gameID, "sync-start", map[string]any{"peerName": deviceName, "direction": "upload"})

	defer func() {
		if retErr != nil {
			e.Transport.ReportSyncEvent(peer, gameID, "sync-error", map[string]any{
				"peerName": deviceName, "error": retErr.Error(), "direction": "upload",
			})
			if e.Progress.OnSyncError != nil {
				e.Progress.OnSyncError(gameID, ProgressEvent{PeerName: peer.Name, Error: retErr.Error()})
			}
		}
	}()

	// Make sure every remote directory exists before patching into it.
	for _, dir := range remoteData.Manifest.Dirs {
		if delta.IsSafePath(root.Path, dir) {
			_ = os.MkdirAll(filepath.Join(root.Path, filepath.FromSlash(dir)), 0o777)
		}
	}

	// Pre-compute per-file changed blocks, the total transfer byte count,
	// and the disk footprint (net growth + largest single new file, since
	// PatchFile writes a temp copy before renaming over the old version).
	changedBlocks := map[string][]int{}
	var totalBytes, netGrowth, maxNewSize int64
	for _, relPath := range filesToPull {
		remoteFile := remoteData.Manifest.Files[relPath]
		var localFile *delta.FileEntry
		var localSize int64
		if lf, ok := localManifest.Files[relPath]; ok {
			localFile = &lf
			localSize = lf.Size
		}
		netGrowth += remoteFile.Size - localSize
		if remoteFile.Size > maxNewSize {
			maxNewSize = remoteFile.Size
		}
		indices := DifferentBlockIndices(localFile, remoteFile)
		changedBlocks[relPath] = indices
		for _, idx := range indices {
			if idx < len(remoteFile.Blocks) {
				totalBytes += int64(remoteFile.Blocks[idx].Length)
			}
		}
	}

	// Fail early with a clear message if the drive can't hold the incoming
	// files, instead of crashing mid-write with a raw OS "disk full" error.
	if netGrowth < 0 {
		netGrowth = 0
	}
	const diskMargin = 16 << 20 // 16 MiB headroom
	needed := netGrowth + maxNewSize + diskMargin
	spaceDir := root.Path
	if fi, err := os.Stat(spaceDir); err != nil || !fi.IsDir() {
		spaceDir = filepath.Dir(root.Path)
	}
	if avail, ok := availableDiskBytes(spaceDir); ok && uint64(needed) > avail {
		return fmt.Errorf("not enough free storage: this sync needs about %s but only %s is free on the drive holding your save",
			humanBytes(needed), humanBytes(int64(avail)))
	}

	tracker := newProgressTracker(totalBytes)
	throttle := e.throttleFor(peer.Wan())

	// Progress reporter shared by the per-file loop and the block-group
	// loop inside each file. Without in-file reporting, a single large
	// file (e.g. an 18MB save pulled over the relay) sat at 0% for its
	// whole transfer. Throttled so relay round-trips don't spam the UI.
	// Guarded because block workers report progress concurrently.
	var reportMu sync.Mutex
	var lastReport time.Time
	reportProgress := func(force bool) {
		reportMu.Lock()
		if !force && time.Since(lastReport) < 500*time.Millisecond {
			reportMu.Unlock()
			return
		}
		lastReport = time.Now()
		reportMu.Unlock()

		bytesPulled, speed, pct := tracker.stats()
		ev := ProgressEvent{PeerName: peer.Name, BytesTransferred: bytesPulled, TotalBytes: totalBytes, SpeedBytesPerSec: speed, Percentage: pct}
		if e.Progress.OnSyncProgress != nil {
			e.Progress.OnSyncProgress(gameID, ev)
		}
		e.Transport.ReportSyncEvent(peer, gameID, "sync-progress", map[string]any{
			"peerName": deviceName, "bytesTransferred": bytesPulled, "totalBytes": totalBytes,
			"speedBytesPerSec": speed, "percentage": pct,
		})
	}

	for _, relPath := range filesToPull {
		if !delta.IsSafePath(root.Path, relPath) {
			return fmt.Errorf("path traversal attempt on pulled file %s", relPath)
		}
		remoteFile := remoteData.Manifest.Files[relPath]
		indices := changedBlocks[relPath]

		localFilePath := filepath.Join(root.Path, filepath.FromSlash(relPath))
		if isFile, _ := delta.ResolveLocalSaveFilePath(root.Path); isFile {
			localFilePath = root.Path // single-file save mode
		}
		if err := e.pullFile(ctx, peer, FileRef{GameID: gameID, Root: root.Name, RelPath: relPath}, localFilePath,
			remoteFile, indices, throttle, tracker, reportProgress); err != nil {
			return err
		}
		if remoteFile.MtimeMs > 0 {
			mtime := time.UnixMilli(int64(remoteFile.MtimeMs))
			_ = os.Chtimes(localFilePath, mtime, mtime)
		}
		e.Log("info", "file updated: "+relPath)

		// File-boundary progress reporting (always fires).
		reportProgress(true)
	}

	// Mirror the peer's latest snapshot locally so both sides share history.
	if remoteData.LatestSnapshot != nil {
		e.recordMirrorSnapshot(gameID, game, peer, *remoteData.LatestSnapshot,
			fmt.Sprintf("Synced from peer: %s (%s)", peer.Name, remoteData.LatestSnapshot.Comment))
	}

	e.Transport.ReportSyncEvent(peer, gameID, "sync-complete", map[string]any{"peerName": deviceName, "direction": "upload"})
	if e.Progress.OnSyncComplete != nil {
		e.Progress.OnSyncComplete(gameID, ProgressEvent{PeerName: peer.Name, Direction: "download"})
	}
	return nil
}

// pullFile reconstructs one file, writing each batch of blocks to disk as it
// arrives rather than collecting them all first. Memory stays proportional to
// the blocks in flight instead of to the file — a 1 GB save used to need 1 GB
// of RAM before a single byte was written.
func (e *Engine) pullFile(ctx context.Context, peer Peer, ref FileRef, localFilePath string,
	remoteFile delta.FileEntry, indices []int, throttle *throttler, tracker *progressTracker,
	onProgress func(force bool)) error {

	relPath := ref.RelPath

	writer, err := delta.NewPatchWriter(localFilePath, remoteFile)
	if err != nil {
		return fmt.Errorf("patch %s: %w", relPath, err)
	}
	committed := false
	defer func() {
		if !committed {
			writer.Abort()
		}
	}()

	incoming := make(map[int]bool, len(indices))
	for _, idx := range indices {
		incoming[idx] = true
	}
	// Blocks that didn't change come straight from the copy already on disk.
	if err := writer.SeedUnchanged(localFilePath, incoming); err != nil {
		return fmt.Errorf("patch %s: %w", relPath, err)
	}

	if err := e.fetchFileBlocks(ctx, peer, ref, remoteFile, indices,
		throttle, tracker, onProgress, writer); err != nil {
		return err
	}

	if err := writer.Commit(); err != nil {
		return fmt.Errorf("patch %s: %w", relPath, err)
	}
	committed = true
	return nil
}

// fetchFileBlocks pulls one file's changed blocks into writer using a pool of
// workers. The previous version processed batches in fixed groups and waited
// at every group boundary, so the slowest request in each group stalled the
// rest — costly over a relay, where one slow round trip is common. A pool
// keeps every slot busy until the work runs out.
func (e *Engine) fetchFileBlocks(ctx context.Context, peer Peer, ref FileRef,
	remoteFile delta.FileEntry, indices []int, throttle *throttler, tracker *progressTracker,
	onProgress func(force bool), writer *delta.PatchWriter) error {

	relPath := ref.RelPath

	batches := BatchIndices(indices, remoteFile.BlockSize, peer.Wan())
	if len(batches) == 0 {
		return nil
	}
	concurrency := ConcurrencyFor(peer.Wan())
	if concurrency > len(batches) {
		concurrency = len(batches)
	}

	// Cancelled as soon as any worker fails, so the rest stop instead of
	// finishing transfers whose result is going to be thrown away.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}

	work := make(chan []int)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range work {
				blocks, err := fetchWithRetry(ctx, e.Transport, peer, ref, batch, remoteFile.BlockSize, e.Log)
				if err != nil {
					fail(fmt.Errorf("fetch blocks for %s: %w", relPath, err))
					return
				}
				var batchBytes int64
				for _, b := range blocks {
					if err := writer.WriteBlock(b.Index, b.Data); err != nil {
						fail(fmt.Errorf("patch %s: %w", relPath, err))
						return
					}
					batchBytes += int64(b.Length)
				}
				tracker.add(batchBytes)
				if onProgress != nil {
					onProgress(false) // in-file progress so big files don't sit at 0%
				}
				throttle.wait(ctx, batchBytes)
			}
		}()
	}

	for _, batch := range batches {
		select {
		case work <- batch:
		case <-ctx.Done():
			// A worker failed (or the sync was cancelled); stop feeding it.
		}
	}
	close(work)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	// A cancelled parent context with no worker error still means the pull
	// didn't finish; Commit would otherwise fail with a confusing hash error.
	return ctx.Err()
}

func fetchWithRetry(ctx context.Context, t Transport, peer Peer, ref FileRef,
	indices []int, blockSize int, logf func(string, string)) ([]BlockData, error) {

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		blocks, err := t.FetchBlocks(ctx, peer, ref, indices, blockSize)
		if err == nil {
			return blocks, nil
		}
		lastErr = err
		logf("warn", fmt.Sprintf("block fetch attempt %d/%d failed for %s: %v", attempt, maxAttempts, ref.RelPath, err))
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second): // linear backoff
			}
		}
	}
	return nil, lastErr
}

// recordMirrorSnapshot zips the (just-updated) local save under the peer's
// snapshot id so both devices show the same history entry.
func (e *Engine) recordMirrorSnapshot(gameID string, game store.Game, peer Peer, remoteSnap SnapshotInfo, comment string) {
	if _, err := e.Store.GetSnapshot(remoteSnap.ID); err == nil {
		return // already mirrored
	}

	settings, err := e.Store.GetSettings()
	if err != nil {
		return
	}
	backupsDir := settings.SyncBackupsDir
	if backupsDir == "" {
		backupsDir = settings.BackupsDir
	}
	destDir := filepath.Join(backupsDir, gameID, game.ActiveBranch)
	if err := os.MkdirAll(destDir, 0o777); err != nil {
		return
	}
	zipPath := filepath.Join(destDir, remoteSnap.ID+".zip")
	// Every save location, like any other snapshot. Archiving only the main
	// folder here would quietly put entries in the history that hold half the
	// game — and nothing about them would look different, so someone rolling
	// back to one would find their settings folder untouched and no
	// explanation for it.
	mirrorRoots, rootsErr := e.Store.GameRootPaths(gameID)
	if rootsErr != nil {
		mirrorRoots = nil
	}
	if _, err := snapshot.ZipRoots(game.SavePath, mirrorRoots, zipPath); err != nil {
		e.Log("warn", fmt.Sprintf("mirror snapshot zip failed: %v", err))
		return
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		return
	}

	_ = e.Store.CreateSnapshot(store.Snapshot{
		ID:           remoteSnap.ID,
		GameID:       gameID,
		BranchName:   game.ActiveBranch,
		Timestamp:    remoteSnap.Timestamp,
		Comment:      comment,
		IsSystemAuto: true,
		ZipPath:      zipPath,
		SizeBytes:    info.Size(),
	})
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (e *Engine) deviceName() string {
	settings, err := e.Store.GetSettings()
	if err != nil {
		return "OpenSave"
	}
	return settings.DeviceName
}

// throttler enforces the WAN speed limit by pausing after each batch
// proportionally to the bytes just transferred (delay = bytes / limit).
//
// Blocks are fetched by several workers at once, so the pacing has to be
// shared: if each worker just slept for its own batch they would sleep in
// parallel and the link would run at concurrency x the configured limit.
// Reserving slots on a single timeline keeps the aggregate rate honest.
type throttler struct {
	limitBytesPerSec int64

	mu       sync.Mutex
	nextFree time.Time
}

func (e *Engine) throttleFor(isWan bool) *throttler {
	if !isWan {
		return &throttler{}
	}
	settings, err := e.Store.GetSettings()
	if err != nil || settings.SpeedLimitKbps <= 0 {
		return &throttler{}
	}
	return &throttler{limitBytesPerSec: int64(settings.SpeedLimitKbps) * 1024}
}

func (t *throttler) wait(ctx context.Context, bytes int64) {
	if t.limitBytesPerSec <= 0 || bytes <= 0 {
		return
	}
	delay := time.Duration(bytes * int64(time.Second) / t.limitBytesPerSec)

	// Claim this batch's slice of the timeline, then sleep until it starts.
	// Sub-50ms debts aren't slept off individually but still accumulate here,
	// so many small batches are paced as accurately as a few large ones.
	t.mu.Lock()
	now := time.Now()
	if t.nextFree.Before(now) {
		t.nextFree = now
	}
	t.nextFree = t.nextFree.Add(delay)
	until := t.nextFree
	t.mu.Unlock()

	remaining := time.Until(until)
	if remaining < 50*time.Millisecond {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(remaining):
	}
}

// progressTracker accumulates transferred bytes and derives speed/percent.
type progressTracker struct {
	mu          sync.Mutex
	start       time.Time
	total       int64
	transferred int64
}

func newProgressTracker(total int64) *progressTracker {
	return &progressTracker{start: time.Now(), total: total}
}

func (p *progressTracker) add(bytes int64) {
	p.mu.Lock()
	p.transferred += bytes
	p.mu.Unlock()
}

func (p *progressTracker) stats() (transferred int64, speedBytesPerSec float64, percentage int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := time.Since(p.start).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(p.transferred) / elapsed
	}
	pct := 100
	if p.total > 0 {
		pct = int(p.transferred * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
	}
	return p.transferred, speed, pct
}
