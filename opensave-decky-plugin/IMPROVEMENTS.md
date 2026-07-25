# Steam Deck & Decky plugin — improvement plan

Findings from an audit of `opensave-decky-plugin/` and the Flatpak packaging,
written up as an actionable list.

> **P0-1, P0-2 and P1-1 are now implemented** (see the checkboxes below), but
> **none of it has run on a Steam Deck**. The plugin builds cleanly and the
> Python parses, which is as far as verification can go off-device. Treat the
> whole thing as untested until someone loads it in Game Mode.

**Anyone picking this up needs a Steam Deck (or a SteamOS VM in Game Mode).**
Decky plugins only load inside Game Mode, so none of this is verifiable on a
desktop — the work can be written and typechecked anywhere, but must be
confirmed on-device before release.

Ordered by priority. P0 items block everything else.

---

## P0-1 — Port the plugin to the Decky v3 API — ✅ DONE

**Was: the plugin did not build or run on current Decky Loader.**

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
- [x] Replace `decky-frontend-lib` imports with `@decky/ui`.
- [x] Replace every `serverApi.callServerMethod(...)` with a `callable<Args, Ret>()`
      declared at module scope.
- [x] Update `definePlugin` to the no-argument signature.
- [x] Add `@decky/ui` and `@decky/api` to `package.json` — they are currently
      marked `external` in `rollup.config.js` but declared nowhere, so a clean
      `npm install && npm run build` cannot resolve their types.
- [x] Consider adopting the template's build setup (it uses its own bundler
      config and `deckyplugin` conventions) rather than the hand-rolled
      `rollup.config.js`.
- [x] Add `"api_version": 1` to `plugin.json`.

### Done when
A clean checkout builds with no network-dependent guesswork, and the panel
loads in Game Mode showing live daemon status.

---

## P0-2 — Make the daemon available in Game Mode — ✅ DONE

**Was the architectural blocker.** The daemon runs *inside the desktop
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
- [x] Add a `start_daemon` backend method in `main.py` that spawns the above
      detached, and returns whether it came up.
- [x] Surface a **Start OpenSave** button in the panel whenever the daemon is
      unreachable, instead of a dead `OFFLINE` label.
- [x] Decide the lifetime model and document it. Both are now supported:
      **on-demand** (the panel's *Start sync service* button) for a zero-setup
      path, and **persistent** via `packaging/systemd/opensave-daemon.service`
      for users who want syncing without ever opening the panel. The unit file
      carries its own install instructions, including `loginctl enable-linger`
      so it survives Game Mode/Desktop Mode switches.
- [x] Handle the "desktop app is already running" case so two daemons never
      contend for the same port.

### Done when
A user who has never opened Desktop Mode can sync from Game Mode.

---

## P1-1 — Sync automatically on game launch and exit — ✅ DONE

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
- [x] Register a lifetime listener in `definePlugin`'s setup, unregister on
      teardown.
- [x] Map the Steam AppID from the event to a tracked game (the daemon already
      stores `appId`; the App-ID matching work is in `internal/store/aliases.go`).
- [~] On launch: **blocking is not achievable and was dropped.** The lifetime
      notification fires once the game is *already running*, so there is no
      point at which a plugin can hold the launch back. The launch-side sync is
      therefore best-effort and fire-and-forget; the **exit** sync is the
      reliable one, and it's the one that matters — it captures what you just
      played. Users wanting a guaranteed-fresh save should sync from the panel
      before launching.
- [x] On exit: trigger a sync, toast the outcome.
- [x] Make both behaviours toggleable in the panel; some users will want manual
      control.

### Risks
- Blocking a game launch is user-hostile if it goes wrong. Cap the wait (a few
  seconds), and fail open — always let the game start.
- Rapid launch/exit cycles shouldn't queue up redundant syncs.

---

## P1-2 — Make the panel do more than report — ✅ MOSTLY DONE

The panel currently offers exactly one action (*Sync All*) and is otherwise
read-only. Notably, **a conflict cannot be resolved from Game Mode at all** —
the user is stuck until they reach Desktop Mode, which on a handheld is a
serious dead end.

### Tasks
- [x] Per-game **Sync now** (`POST /api/games/{id}/sync`).
- [ ] Show live sync progress — the daemon emits progress over its WebSocket;
      the panel currently only reports that a sync started.
- [x] Surface conflicts, with the same three resolutions the desktop app offers.
      `/api/status` now includes active conflicts (they were WebSocket-only, so
      a plain-HTTP client couldn't see them), and the panel offers Keep both /
      Keep this device's / Keep peer's.
- [x] Show last-synced time per game (derived from the newest snapshot).
- [x] A snapshot button, to checkpoint before a risky run.

---

## P2 — Smaller fixes

- [x] **Hardcoded port.** `main.py` pins `http://127.0.0.1:8383`. If the user
      changed the daemon port in Settings the plugin silently breaks. Read the
      port from the daemon's config file, or probe.
- [ ] **Replace 5s polling with events.** `index.tsx` polls two endpoints every
      5 seconds *while the panel is open* (Decky unmounts the content when it
      closes, so this doesn't run in the background). Lower priority than first
      assessed, but a WebSocket bridge plus `decky.emit` would still give live
      progress and cost less battery.
- [x] **Use `decky` module conventions in `main.py`** — now imports `decky` and
      uses `decky.logger` / `decky.DECKY_USER_HOME` instead of writing straight
      to `/tmp/opensave-decky.log`.
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
