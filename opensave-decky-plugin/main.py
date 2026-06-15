import os
import sys
import logging
import urllib.request
import json

# Setup logging inside a writeable directory on SteamOS
log_file = "/tmp/opensave-decky.log"
logging.basicConfig(filename=log_file, format='%(asctime)s %(levelname)s %(message)s', level=logging.INFO)

DAEMON_URL = "http://127.0.0.1:8383"

class Plugin:
    async def get_daemon_status(self):
        """Checks if the OpenSave daemon is active and running."""
        try:
            req = urllib.request.Request(f"{DAEMON_URL}/api/status")
            with urllib.request.urlopen(req, timeout=2) as response:
                return {
                    "running": True,
                    "data": json.loads(response.read().decode())
                }
        except Exception as e:
            logging.error(f"Failed to fetch daemon status: {str(e)}")
            return {
                "running": False,
                "error": f"Daemon unreachable: {str(e)}"
            }

    async def get_games(self):
        """Fetches the list of games and sync status from the daemon."""
        try:
            req = urllib.request.Request(f"{DAEMON_URL}/api/games")
            with urllib.request.urlopen(req, timeout=2) as response:
                return json.loads(response.read().decode())
        except Exception as e:
            logging.error(f"Failed to fetch games list: {str(e)}")
            return {}

    async def trigger_sync_all(self):
        """Triggers direct synchronization of all game saves with online peers."""
        try:
            req = urllib.request.Request(f"{DAEMON_URL}/api/games/sync-all", method="POST")
            with urllib.request.urlopen(req, timeout=15) as response:
                return {
                    "success": True,
                    "results": json.loads(response.read().decode())
                }
        except Exception as e:
            logging.error(f"Failed to trigger sync-all: {str(e)}")
            return {
                "success": False,
                "error": str(e)
            }

    async def _main(self):
        logging.info("OpenSave Decky Loader Plugin Backend Started.")
