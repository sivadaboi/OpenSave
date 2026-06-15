import {
  definePlugin,
  ServerAPI,
  staticClasses,
  PanelSection,
  PanelSectionRow,
  ButtonItem,
} from "decky-frontend-lib";
import React, { VFC, useState, useEffect } from "react";

interface Game {
  id: string;
  name: string;
  activeBranch: string;
  syncStatus?: string;
  createdAt: string;
}

interface StatusData {
  settings: {
    deviceName: string;
    deviceType: string;
    relayUrl: string;
  };
  gamesCount: number;
  peersCount: number;
}

const Content: VFC<{ serverApi: ServerAPI }> = ({ serverApi }) => {
  const [daemonRunning, setDaemonRunning] = useState<boolean>(false);
  const [status, setStatus] = useState<StatusData | null>(null);
  const [games, setGames] = useState<Record<string, Game>>({});
  const [syncing, setSyncing] = useState<boolean>(false);

  const fetchStatus = async () => {
    try {
      const response = await serverApi.callServerMethod("get_daemon_status", {});
      if (response.success && response.result.running) {
        setDaemonRunning(true);
        setStatus(response.result.data);
      } else {
        setDaemonRunning(false);
      }
    } catch (e) {
      setDaemonRunning(false);
    }
  };

  const fetchGames = async () => {
    try {
      const response = await serverApi.callServerMethod("get_games", {});
      if (response.success) {
        setGames(response.result);
      }
    } catch (e) {}
  };

  useEffect(() => {
    fetchStatus();
    fetchGames();
    const timer = setInterval(() => {
      fetchStatus();
      fetchGames();
    }, 5000);
    return () => clearInterval(timer);
  }, []);

  const handleSyncAll = async () => {
    setSyncing(true);
    try {
      await serverApi.callServerMethod("trigger_sync_all", {});
    } catch (e) {
    } finally {
      setSyncing(false);
      fetchGames();
    }
  };

  return (
    <PanelSection title="OpenSave Panel">
      <PanelSectionRow>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>Daemon Status</span>
          <span style={{ 
            color: daemonRunning ? "#2eff76" : "#ff3333", 
            fontWeight: "bold" 
          }}>
            {daemonRunning ? "● ACTIVE" : "● OFFLINE"}
          </span>
        </div>
      </PanelSectionRow>

      {daemonRunning && status && (
        <>
          <PanelSectionRow>
            <div style={{ fontSize: "0.9em", opacity: 0.8 }}>
              <div>Device: <strong>{status.settings.deviceName}</strong></div>
              <div>Peers Connected: <strong>{status.peersCount}</strong></div>
            </div>
          </PanelSectionRow>

          <PanelSectionRow>
            <ButtonItem
              layout="inline"
              onClick={handleSyncAll}
              disabled={syncing}
            >
              {syncing ? "🔄 Syncing..." : "⚡ Sync All Now"}
            </ButtonItem>
          </PanelSectionRow>

          <PanelSection title="Tracked Games">
            {Object.keys(games).length === 0 ? (
              <PanelSectionRow>
                <div style={{ opacity: 0.6, fontSize: "0.9em" }}>No games tracked yet.</div>
              </PanelSectionRow>
            ) : (
              Object.values(games).map((game) => (
                <PanelSectionRow key={game.id}>
                  <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
                    <span style={{ fontWeight: "bold" }}>{game.name}</span>
                    <div style={{ display: "flex", justifyContent: "space-between", fontSize: "0.85em", opacity: 0.8 }}>
                      <span>Branch: {game.activeBranch}</span>
                      <span style={{ 
                        color: game.syncStatus === "synced" ? "#2eff76" : "#ffba00" 
                      }}>
                        {game.syncStatus || "local-only"}
                      </span>
                    </div>
                  </div>
                </PanelSectionRow>
              ))
            )}
          </PanelSection>
        </>
      )}
    </PanelSection>
  );
};

export default definePlugin((serverApi: ServerAPI) => {
  return {
    title: <div className={staticClasses.Title}>OpenSave</div>,
    content: <Content serverApi={serverApi} />,
    icon: <div>⚡</div>,
  };
});
