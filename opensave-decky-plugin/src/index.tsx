import {
  ButtonItem,
  PanelSection,
  PanelSectionRow,
  ToggleField,
  staticClasses,
} from "@decky/ui";
import { callable, definePlugin, toaster } from "@decky/api";
import { useEffect, useState } from "react";
import { FaSave } from "react-icons/fa";

// ── Backend bridge ─────────────────────────────────────────────────────

interface Game {
  id: string;
  name: string;
  activeBranch: string;
  appId?: string;
  savePath?: string;
  branches?: Record<string, { snapshots?: Snapshot[] }>;
}

interface Snapshot {
  id: string;
  timestamp?: string;
}

interface Conflict {
  peer: { id: string; name: string };
  localStats?: { files: number; totalBytes: number; latestMtimeMs: number };
  remoteStats?: { files: number; totalBytes: number; latestMtimeMs: number };
  diffTotal?: number;
}

interface DaemonStatus {
  running: boolean;
  data?: {
    settings?: { deviceName?: string };
    gameCount?: number;
    peerCount?: number;
    conflicts?: Record<string, Conflict>;
    conflictCount?: number;
  };
  error?: string;
}

const getDaemonStatus = callable<[], DaemonStatus>("get_daemon_status");
const getGames = callable<[], Record<string, Game>>("get_games");
const syncAll = callable<[], { success: boolean; error?: string }>("sync_all");
const syncGame = callable<[game_id: string], { success: boolean; error?: string }>("sync_game");
const startDaemon =
  callable<[], { success: boolean; alreadyRunning?: boolean; error?: string }>("start_daemon");
const findGameByAppId =
  callable<[app_id: string], { found: boolean; game?: Game }>("find_game_by_appid");
const snapshotGame =
  callable<[game_id: string, comment: string], { success: boolean; error?: string }>("snapshot_game");
const resolveConflict = callable<
  [game_id: string, peer_id: string, resolution: string],
  { success: boolean; error?: string }
>("resolve_conflict");

/** Newest snapshot across a game's branches, as a human "x ago" string. */
function lastSyncedLabel(game: Game): string {
  let newest = 0;
  for (const branch of Object.values(game.branches ?? {})) {
    for (const snap of branch.snapshots ?? []) {
      const t = Date.parse(snap.timestamp ?? "");
      if (!isNaN(t) && t > newest) newest = t;
    }
  }
  if (!newest) return "no snapshots yet";
  const mins = Math.floor((Date.now() - newest) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

// ── Auto-sync preferences ──────────────────────────────────────────────
// Stored in localStorage rather than the daemon: these govern this device's
// Game Mode behaviour, and must be readable before the daemon is reachable.

const AUTO_SYNC_KEY = "opensave:autoSyncOnGameEvents";

function autoSyncEnabled(): boolean {
  return localStorage.getItem(AUTO_SYNC_KEY) !== "false"; // default on
}
function setAutoSyncEnabled(on: boolean) {
  localStorage.setItem(AUTO_SYNC_KEY, on ? "true" : "false");
}

// ── Panel ──────────────────────────────────────────────────────────────

function Content() {
  const [status, setStatus] = useState<DaemonStatus | null>(null);
  const [games, setGames] = useState<Record<string, Game>>({});
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState(false);
  const [autoSync, setAutoSync] = useState(autoSyncEnabled());

  const refresh = async () => {
    const s = await getDaemonStatus();
    setStatus(s);
    setGames(s.running ? await getGames() : {});
  };

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, 5000);
    return () => clearInterval(timer);
  }, []);

  const onStartDaemon = async () => {
    setStarting(true);
    try {
      const res = await startDaemon();
      if (res.success) {
        toaster.toast({ title: "OpenSave", body: "Sync service started" });
      } else {
        toaster.toast({ title: "OpenSave", body: res.error ?? "Couldn't start the sync service" });
      }
      await refresh();
    } finally {
      setStarting(false);
    }
  };

  const onSyncAll = async () => {
    setBusy(true);
    try {
      const res = await syncAll();
      toaster.toast({
        title: "OpenSave",
        body: res.success ? "Sync started for all games" : res.error ?? "Sync failed",
      });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const onSyncOne = async (game: Game) => {
    setBusy(true);
    try {
      const res = await syncGame(game.id);
      toaster.toast({
        title: game.name,
        body: res.success ? "Sync started" : res.error ?? "Sync failed",
      });
    } finally {
      setBusy(false);
    }
  };

  // Daemon unreachable: offer to start it rather than showing a dead label.
  if (status && !status.running) {
    return (
      <PanelSection title="OpenSave">
        <PanelSectionRow>
          <div style={{ fontSize: "0.9em", opacity: 0.8 }}>
            The sync service isn't running, so saves aren't being synced.
          </div>
        </PanelSectionRow>
        <PanelSectionRow>
          <ButtonItem layout="below" onClick={onStartDaemon} disabled={starting}>
            {starting ? "Starting…" : "Start sync service"}
          </ButtonItem>
        </PanelSectionRow>
      </PanelSection>
    );
  }

  const onSnapshot = async (game: Game) => {
    setBusy(true);
    try {
      const res = await snapshotGame(game.id, "Snapshot from Game Mode");
      toaster.toast({
        title: game.name,
        body: res.success ? "Snapshot taken" : res.error ?? "Snapshot failed",
      });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const onResolve = async (gameId: string, peerId: string, resolution: string, label: string) => {
    setBusy(true);
    try {
      const res = await resolveConflict(gameId, peerId, resolution);
      toaster.toast({
        title: "OpenSave",
        body: res.success ? `Resolving: ${label}…` : res.error ?? "Could not resolve",
      });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const list = Object.values(games);
  const conflicts = Object.entries(status?.data?.conflicts ?? {});

  return (
    <PanelSection title="OpenSave">
      <PanelSectionRow>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>{status?.data?.settings?.deviceName ?? "This device"}</span>
          <span style={{ color: "#2eff76", fontWeight: "bold" }}>● ONLINE</span>
        </div>
      </PanelSectionRow>
      <PanelSectionRow>
        <div style={{ fontSize: "0.85em", opacity: 0.75 }}>
          {list.length} game{list.length === 1 ? "" : "s"} tracked ·{" "}
          {status?.data?.peerCount ?? 0} peer{(status?.data?.peerCount ?? 0) === 1 ? "" : "s"} online
        </div>
      </PanelSectionRow>

      <PanelSectionRow>
        <ButtonItem layout="below" onClick={onSyncAll} disabled={busy || list.length === 0}>
          {busy ? "Syncing…" : "Sync all now"}
        </ButtonItem>
      </PanelSectionRow>

      {/* A conflict stops a game syncing until someone chooses a side. Without
          this the only way out was Desktop Mode. */}
      {conflicts.length > 0 && (
        <PanelSection title={`Needs a decision (${conflicts.length})`}>
          {conflicts.map(([gameId, c]) => {
            const game = games[gameId];
            const mine = c.localStats?.latestMtimeMs ?? 0;
            const theirs = c.remoteStats?.latestMtimeMs ?? 0;
            const newer = mine === theirs ? "same age" : mine > theirs ? "yours is newer" : "theirs is newer";
            return (
              <div key={gameId}>
                <PanelSectionRow>
                  <div style={{ fontSize: "0.85em" }}>
                    <div style={{ fontWeight: "bold" }}>{game?.name ?? gameId}</div>
                    <div style={{ opacity: 0.75 }}>
                      Both this device and {c.peer?.name ?? "a peer"} changed this save
                      {c.diffTotal ? ` (${c.diffTotal} file${c.diffTotal === 1 ? "" : "s"} differ)` : ""} — {newer}.
                    </div>
                  </div>
                </PanelSectionRow>
                <PanelSectionRow>
                  <ButtonItem
                    layout="below"
                    disabled={busy}
                    onClick={() => onResolve(gameId, c.peer.id, "merge-branch", "keeping both")}
                    description="Safest: keeps both saves, theirs on a separate branch."
                  >
                    Keep both
                  </ButtonItem>
                </PanelSectionRow>
                <PanelSectionRow>
                  <ButtonItem
                    layout="below"
                    disabled={busy}
                    onClick={() => onResolve(gameId, c.peer.id, "keep-local", "keeping this device's save")}
                  >
                    Keep this device's
                  </ButtonItem>
                </PanelSectionRow>
                <PanelSectionRow>
                  <ButtonItem
                    layout="below"
                    disabled={busy}
                    onClick={() => onResolve(gameId, c.peer.id, "keep-remote", `keeping ${c.peer?.name ?? "the peer"}'s save`)}
                  >
                    Keep {c.peer?.name ?? "peer"}'s
                  </ButtonItem>
                </PanelSectionRow>
              </div>
            );
          })}
        </PanelSection>
      )}

      <PanelSectionRow>
        <ToggleField
          label="Sync around gameplay"
          description="Pull the latest save before a game starts, and push it again when you quit."
          checked={autoSync}
          onChange={(v: boolean) => {
            setAutoSync(v);
            setAutoSyncEnabled(v);
          }}
        />
      </PanelSectionRow>

      <PanelSection title="Tracked games">
        {list.length === 0 ? (
          <PanelSectionRow>
            <div style={{ opacity: 0.6, fontSize: "0.9em" }}>
              Nothing tracked yet — add games from Desktop Mode.
            </div>
          </PanelSectionRow>
        ) : (
          list.map((game) => (
            <div key={game.id}>
              <PanelSectionRow>
                <ButtonItem
                  layout="below"
                  onClick={() => onSyncOne(game)}
                  disabled={busy}
                  description={`Last snapshot ${lastSyncedLabel(game)} · branch ${game.activeBranch}`}
                >
                  {game.name}
                </ButtonItem>
              </PanelSectionRow>
              <PanelSectionRow>
                <ButtonItem
                  layout="below"
                  onClick={() => onSnapshot(game)}
                  disabled={busy}
                  description="Checkpoint the current save before a risky run."
                >
                  Snapshot
                </ButtonItem>
              </PanelSectionRow>
            </div>
          ))
        )}
      </PanelSection>
    </PanelSection>
  );
}

// ── Game lifecycle hooks ───────────────────────────────────────────────
// The point of a Game Mode plugin: sync at the moments saves actually
// matter, so nobody has to remember to open this panel.

function registerLifecycleHooks(): () => void {
  // SteamClient is a global declared by @decky/ui, but Game Mode is the only
  // place it exists — guard so a desktop/dev context degrades quietly.
  const client = (globalThis as any).SteamClient;
  if (!client?.GameSessions?.RegisterForAppLifetimeNotifications) {
    console.warn("[OpenSave] SteamClient.GameSessions unavailable; auto-sync disabled");
    return () => {};
  }

  // Guards against a game's rapid start/stop churn queueing duplicate syncs.
  const inFlight = new Set<string>();

  const syncFor = async (appId: string, when: "launch" | "exit") => {
    if (!autoSyncEnabled() || inFlight.has(appId)) return;
    inFlight.add(appId);
    try {
      // Starting the daemon on demand would delay a game launch, so only act
      // when it's already up.
      if (!(await getDaemonStatus()).running) return;

      const match = await findGameByAppId(appId);
      if (!match.found || !match.game) return;

      const res = await syncGame(match.game.id);
      if (!res.success) {
        toaster.toast({ title: match.game.name, body: `Save sync failed: ${res.error ?? ""}` });
        return;
      }
      toaster.toast({
        title: match.game.name,
        body: when === "launch" ? "Save synced before launch" : "Save synced",
      });
    } catch (e) {
      console.error("[OpenSave] lifecycle sync failed", e);
    } finally {
      inFlight.delete(appId);
    }
  };

  const unregister = client.GameSessions.RegisterForAppLifetimeNotifications(
    (update: { unAppID: number; bRunning: boolean }) => {
      const appId = String(update.unAppID);
      // Deliberately fire-and-forget: a game launch must never wait on the
      // network. The sync runs alongside it.
      void syncFor(appId, update.bRunning ? "launch" : "exit");
    },
  );

  return () => {
    try {
      unregister?.unregister?.();
    } catch {
      /* nothing useful to do if Steam already tore it down */
    }
  };
}

export default definePlugin(() => {
  const disposeHooks = registerLifecycleHooks();

  return {
    name: "OpenSave",
    title: <div className={staticClasses.Title}>OpenSave</div>,
    content: <Content />,
    icon: <FaSave />,
    onDismount() {
      disposeHooks();
    },
  };
});
