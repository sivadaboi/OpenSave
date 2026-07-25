# Steam Deck & Decky plugin — improvement plan

Findings from an audit of `opensave-decky-plugin/` and the Flatpak packaging,
written up as an actionable list. Nothing here has been implemented.

**Anyone picking this up needs a Steam Deck (or a SteamOS VM in Game Mode).**
Decky plugins only load inside Game Mode, so none of this is verifiable on a
desktop — the work can be written and typechecked anywhere, but must be
confirmed on-device before release.

Ordered by priority. P0 items block everything else.

---

## P0-1 — Port the plugin to the Decky v3 API

**Status: the plugin almost certainly does not run on current Decky Loader.**

`src/index.tsx` is written against the legacy API. Compared with the current
[official template](https://github.com/SteamDeckHomebrew/decky-plugin-template):

| Current plugin | Required now |
| --- | --- |
| `import … from "decky-frontend-lib"` | `import … from "@decky/ui"` |
| `serverApi.callServerMethod("get_games", {})` | `callable<[], T>("get_games")` |
| `definePlugin((serverApi: ServerAPI) => …)` | `definePlugin(() => …)` |
| — | `toaster` for notifications |
| — | `addEventListener` for backend→frontend events |

`callServerMethod` was **removed** in Decky Loader v3, so `fetchStatus`,
`fetchGames` and `handleSyncAll` all fail at runtime.

### Tasks
- [ ] Replace `decky-frontend-lib` imports with `@decky/ui`.
- [ ] Replace every `serverApi.callServerMethod(...)` with a `callable<Args, Ret>()`
      declared at module scope.
- [ ] Update `definePlugin` to the no-argument signature.
- [ ] Add `@decky/ui` and `@decky/api` to `package.json` — they are currently
      marked `external` in `rollup.config.js` but declared nowhere, so a clean
      `npm install && npm run build` cannot resolve their types.
- [ ] Consider adopting the template's build setup (it uses its own bundler
      config and `deckyplugin` conventions) rather than the hand-rolled
      `rollup.config.js`.
- [ ] Add `"api_version": 1` to `plugin.json`.

### Done when
A clean checkout builds with no network-dependent guesswork, and the panel
loads in Game Mode showing live daemon status.

---

## P0-2 — Make the daemon available in Game Mode

**This is the architectural blocker.** The daemon runs *inside the desktop
app*. In Game Mode that app isn't running, so the panel shows `● OFFLINE`
and the plugin can do nothing about it.

The Flatpak already installs the headless CLI (see
`packaging/flatpak/io.github.sivadaboi.OpenSave.yml`):

```
install -Dm755 opensave-cli /app/bin/opensave-cli
```

so the backend can start a daemon itself:

```bash
flatpak run --command=opensave-cli io.github.sivadaboi.OpenSave daemon
```

### Tasks
- [ ] Add a `start_daemon` backend method in `main.py` that spawns the above
      detached, and returns whether it came up.
- [ ] Surface a **Start OpenSave** button in the panel whenever the daemon is
      unreachable, instead of a dead `OFFLINE` label.
- [ ] Decide the lifetime model and document it:
      - on-demand (plugin starts it when the panel opens), or
      - persistent (a `systemd --user` unit, surviving Game Mode restarts).
      A systemd unit is the better end state — saves should sync whether or not
      anyone opened the panel.
- [ ] Handle the "desktop app is already running" case so two daemons never
      contend for the same port.

### Done when
A user who has never opened Desktop Mode can sync from Game Mode.

---

## P1-1 — Sync automatically on game launch and exit

The highest-value Deck feature, and the reason to have a plugin at all rather
than just the desktop app.

Decky can subscribe to Steam's app lifetime notifications
(`SteamClient.GameSessions.RegisterForAppLifetimeNotifications`). That makes
the natural flow possible:

- **before a game starts** — pull the newest save from peers
- **after it exits** — push the save that was just written

Today the user has to remember to open the panel and press *Sync All*, which
is exactly the manual step OpenSave exists to remove.

### Tasks
- [ ] Register a lifetime listener in `definePlugin`'s setup, unregister on
      teardown.
- [ ] Map the Steam AppID from the event to a tracked game (the daemon already
      stores `appId`; the App-ID matching work is in `internal/store/aliases.go`).
- [ ] On launch: trigger a sync and **block briefly** with a toast if a sync is
      in flight, so the game doesn't start on a stale save. Needs a timeout —
      never hang a game launch on a dead peer.
- [ ] On exit: trigger a sync, toast the outcome.
- [ ] Make both behaviours toggleable in the panel; some users will want manual
      control.

### Risks
- Blocking a game launch is user-hostile if it goes wrong. Cap the wait (a few
  seconds), and fail open — always let the game start.
- Rapid launch/exit cycles shouldn't queue up redundant syncs.

---

## P1-2 — Make the panel do more than report

The panel currently offers exactly one action (*Sync All*) and is otherwise
read-only. Notably, **a conflict cannot be resolved from Game Mode at all** —
the user is stuck until they reach Desktop Mode, which on a handheld is a
serious dead end.

### Tasks
- [ ] Per-game **Sync now** (`POST /api/games/{id}/sync`).
- [ ] Show sync progress — the daemon emits progress over its WebSocket.
- [ ] Surface conflicts, with the same resolution choices the desktop app
      offers (`internal/p2p/syncengine/conflict.go`).
- [ ] Show last-synced time per game.
- [ ] Optional: a snapshot button, so a user can checkpoint before a risky run.

---

## P2 — Smaller fixes

- [ ] **Hardcoded port.** `main.py` pins `http://127.0.0.1:8383`. If the user
      changed the daemon port in Settings the plugin silently breaks. Read the
      port from the daemon's config file, or probe.
- [ ] **Replace 5s polling with events.** `index.tsx` polls two endpoints every
      5 seconds for as long as the panel is open. The daemon already exposes a
      WebSocket; Decky supports `addEventListener` for push updates from the
      Python backend. Less battery, fresher data.
- [ ] **Use `decky` module conventions in `main.py`** — logging currently writes
      straight to `/tmp/opensave-decky.log` rather than using Decky's logger and
      plugin directories.
- [ ] **Cover art** in the panel; the daemon serves it at `/api/cover?appId=`.
- [ ] **Plugin store submission** — if it's meant to be installable from
      Decky's store, it needs the store's metadata and review process.

---

## Flatpak / SteamOS notes

Current `finish-args` look right for the job: `--filesystem=home` covers Steam
`userdata`/`compatdata` and Flatpak-installed emulators under `~/.var/app`, and
`--filesystem=/run/media` covers SD cards. Worth checking on-device:

- [ ] Games or emulators on drives mounted somewhere other than `/run/media`
      (some users mount under `/mnt`).
- [ ] Whether the tray `--own-name=org.kde.StatusNotifierItem-2-1` PID
      assumption still holds — it hardcodes PID 2, which is fragile if the
      sandbox's process layout ever changes.
- [ ] That the runtime bump to GNOME 49 still resolves `webkit2gtk-4.1` (CI has
      an `ldd` smoke test for this; keep it).

---

## Suggested order

1. **P0-1** — nothing works until the API port is done.
2. **P0-2** — makes the plugin useful at all in Game Mode.
3. **P1-1** — the feature that makes it feel native to the Deck.
4. **P1-2**, then **P2**.
