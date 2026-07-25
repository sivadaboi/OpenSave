"""OpenSave Decky Loader backend.

Bridges the Game Mode panel to the OpenSave daemon's local HTTP API, and can
start the daemon itself — in Game Mode the desktop app isn't running, so
without this the panel could only ever report that nothing is available.
"""

import asyncio
import json
import os
import shutil
import subprocess
import urllib.error
import urllib.request

import decky

# Where the daemon publishes the address it actually bound. The configured
# port can be taken, in which case the daemon falls back to an ephemeral one,
# so this file — not the setting — is the source of truth.
ADDR_FILE = os.path.join(decky.DECKY_USER_HOME, ".opensave", "daemon.addr")
FALLBACK_URL = "http://127.0.0.1:8383"

FLATPAK_APP_ID = "io.github.sivadaboi.OpenSave"

# How long to wait for a freshly started daemon to answer.
DAEMON_START_TIMEOUT = 20.0


def _daemon_url() -> str:
    """Base URL of the running daemon, preferring the address it published."""
    try:
        with open(ADDR_FILE, "r", encoding="utf-8") as fh:
            addr = fh.read().strip()
        if addr:
            return f"http://{addr}"
    except OSError:
        pass
    return FALLBACK_URL


def _request(path: str, method: str = "GET", body=None, timeout: float = 10.0):
    req = urllib.request.Request(
        _daemon_url() + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else None


class Plugin:
    # ── Status ───────────────────────────────────────────────────────────

    async def get_daemon_status(self) -> dict:
        """Whether the daemon is reachable, plus its status payload."""
        try:
            return {"running": True, "data": _request("/api/status", timeout=3)}
        except Exception as e:
            return {"running": False, "error": str(e)}

    async def get_games(self) -> dict:
        try:
            return _request("/api/games", timeout=5) or {}
        except Exception as e:
            decky.logger.warning(f"get_games failed: {e}")
            return {}

    # ── Actions ──────────────────────────────────────────────────────────

    async def sync_all(self) -> dict:
        try:
            _request("/api/games/sync-all", method="POST", body={}, timeout=30)
            return {"success": True}
        except Exception as e:
            decky.logger.error(f"sync_all failed: {e}")
            return {"success": False, "error": str(e)}

    async def sync_game(self, game_id: str) -> dict:
        """Sync one game. Used by the panel and by the launch/exit hooks."""
        try:
            _request(f"/api/games/{game_id}/sync", method="POST", body={}, timeout=30)
            return {"success": True}
        except Exception as e:
            decky.logger.error(f"sync_game({game_id}) failed: {e}")
            return {"success": False, "error": str(e)}

    async def snapshot_game(self, game_id: str, comment: str = "") -> dict:
        try:
            _request(
                f"/api/games/{game_id}/snapshot",
                method="POST",
                body={"comment": comment or "Steam Deck snapshot"},
                timeout=60,
            )
            return {"success": True}
        except Exception as e:
            decky.logger.error(f"snapshot_game({game_id}) failed: {e}")
            return {"success": False, "error": str(e)}

    async def resolve_conflict(self, game_id: str, peer_id: str, resolution: str) -> dict:
        """Settle a sync conflict from Game Mode.

        Without this a conflict can only be cleared from Desktop Mode, which on
        a handheld means the game stops syncing until you find a keyboard.
        The daemon applies the resolution in the background — pulling a peer's
        whole save can take minutes — so this returns as soon as it is accepted.
        """
        if resolution not in ("keep-local", "keep-remote", "merge-branch"):
            return {"success": False, "error": f"unknown resolution {resolution}"}
        try:
            _request(
                f"/api/games/{game_id}/resolve-conflict",
                method="POST",
                body={"peerId": peer_id, "resolution": resolution},
                timeout=30,
            )
            return {"success": True}
        except Exception as e:
            decky.logger.error(f"resolve_conflict({game_id}, {resolution}) failed: {e}")
            return {"success": False, "error": str(e)}

    async def find_game_by_appid(self, app_id: str) -> dict:
        """Map a Steam AppID to a tracked game, for the lifecycle hooks."""
        games = await self.get_games()
        for game in games.values():
            if str(game.get("appId") or "") == str(app_id):
                return {"found": True, "game": game}
        return {"found": False}

    # ── Daemon lifecycle ─────────────────────────────────────────────────

    async def start_daemon(self) -> dict:
        """Start a headless daemon.

        Game Mode never runs the desktop app, so the panel needs to be able to
        bring the daemon up itself. Prefers a native opensave-cli on PATH and
        falls back to the Flatpak, which ships the same binary.
        """
        status = await self.get_daemon_status()
        if status.get("running"):
            return {"success": True, "alreadyRunning": True}

        cmd = self._daemon_command()
        if cmd is None:
            return {
                "success": False,
                "error": "OpenSave isn't installed on this device — install the Flatpak from Desktop Mode first.",
            }

        try:
            decky.logger.info(f"starting daemon: {' '.join(cmd)}")
            subprocess.Popen(
                cmd,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                stdin=subprocess.DEVNULL,
                start_new_session=True,  # survives the plugin being reloaded
            )
        except Exception as e:
            decky.logger.error(f"could not start daemon: {e}")
            return {"success": False, "error": str(e)}

        # Wait for it to answer rather than reporting success optimistically.
        deadline = asyncio.get_event_loop().time() + DAEMON_START_TIMEOUT
        while asyncio.get_event_loop().time() < deadline:
            await asyncio.sleep(0.5)
            if (await self.get_daemon_status()).get("running"):
                return {"success": True, "alreadyRunning": False}
        return {"success": False, "error": "The daemon didn't start within 20 seconds."}

    def _daemon_command(self):
        """The best available way to run the headless daemon, or None."""
        native = shutil.which("opensave-cli")
        if native:
            return [native, "daemon"]
        flatpak = shutil.which("flatpak")
        if flatpak:
            return [flatpak, "run", "--command=opensave-cli", FLATPAK_APP_ID, "daemon"]
        return None

    # ── Decky lifecycle ──────────────────────────────────────────────────

    async def _main(self):
        self.loop = asyncio.get_event_loop()
        decky.logger.info("OpenSave plugin loaded")

    async def _unload(self):
        # The daemon is deliberately left running: it is a background sync
        # service, not something that should stop when the panel closes.
        decky.logger.info("OpenSave plugin unloaded")

    async def _uninstall(self):
        decky.logger.info("OpenSave plugin uninstalled")
