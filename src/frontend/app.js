// OpenSave Dashboard App Orchestrator - Sidebar Navigation Edition

let ws = null;
let appState = {
  settings: {},
  games: {},
  peers: {},
  discoveredPeers: [],
  pairingRequests: [],
  wanRoom: null
};

// Public OpenSave cloud relay — no setup needed
const CLOUD_RELAY_URL = 'wss://opensave-relay.onrender.com';
let relayHealthTimer = null;
let activeGameId = null;
let discoveredSavesList = [];
let activeConflictData = null;
let pendingRollbackSnapId = null;
let localCustomScanPaths = [];
let localPathTranslations = [];

// ============================================================
// DOM REFS
// ============================================================
const localDeviceName    = document.getElementById('local-device-name');
const daemonStatusText   = document.getElementById('daemon-status-text');
const titlebarStatus     = document.getElementById('titlebar-status');
const titlebarRelayStatus = document.getElementById('titlebar-relay-status');
const relayStatusText     = document.getElementById('relay-status-text');

const statGamesCount     = document.getElementById('stat-games-count');
const statPeersCount     = document.getElementById('stat-peers-count');
const statBackupsCount   = document.getElementById('stat-backups-count');
const statSyncStatus     = document.getElementById('stat-sync-status');

const gamesGrid          = document.getElementById('games-grid');
const scanResultsGrid    = document.getElementById('scan-results-grid');
const pairedPeersList    = document.getElementById('paired-peers-list');
const discoveredPeersList= document.getElementById('discovered-peers-list');
const pairingRequestsPanel = document.getElementById('pairing-requests-panel');
const pairingRequestsList  = document.getElementById('pairing-requests-list');

const formAddGame        = document.getElementById('form-add-game');
const gameNameInput      = document.getElementById('game-name-input');
const gamePathInput      = document.getElementById('game-path-input');
const formAddPeer        = document.getElementById('form-add-peer');
const peerAddressInput   = document.getElementById('peer-address-input');
const peerPortInput      = document.getElementById('peer-port-input');
const peerProbeStatus    = document.getElementById('peer-probe-status');
const formCreateBranch   = document.getElementById('form-create-branch');
const branchNameInput    = document.getElementById('branch-name-input');
const formSnapshotComment= document.getElementById('form-snapshot-comment');
const snapshotCommentInput = document.getElementById('snapshot-comment-input');

const formWanSync        = document.getElementById('form-wan-sync');
const wanCodeInput       = document.getElementById('wan-code-input');
const activeWanRoomDisplay = document.getElementById('active-wan-room-display');
const wanRoomNameLbl     = document.getElementById('wan-room-name-lbl');
const btnLeaveWanRoom    = document.getElementById('btn-leave-wan-room');
const btnGenerateWanCode = document.getElementById('btn-generate-wan-code');
const wanConnectionState = document.getElementById('wan-connection-state');
const wanRelayUrlLbl     = document.getElementById('wan-relay-url-lbl');
const wanRoomPeersList   = document.getElementById('wan-room-peers-list');
const wanRoomPeerCount   = document.getElementById('wan-room-peer-count');
const wanRelayUrlInput   = document.getElementById('wan-relay-url-input');
const wanHostRelay       = document.getElementById('wan-host-relay');
const wanRelayPortInput  = document.getElementById('wan-relay-port-input');
const wanLocalIps        = document.getElementById('wan-local-ips');
const wanPublicIp        = document.getElementById('wan-public-ip');
const btnSaveWanHosting  = document.getElementById('btn-save-wan-hosting');
const btnUseLocalRelay   = document.getElementById('btn-use-local-relay');

const formUpdateSettings = document.getElementById('form-update-settings');
const settingsDeviceName = document.getElementById('settings-device-name');
const settingsStartOnBoot = document.getElementById('settings-start-on-boot');
const settingsSpeedLimit = document.getElementById('settings-speed-limit');
const settingsRelayUrl   = document.getElementById('settings-relay-url');
const settingsHostRelay  = document.getElementById('settings-host-relay');
const settingsRelayPort  = document.getElementById('settings-relay-port');
const relayPortConfigContainer  = document.getElementById('relay-port-config-container');
const relayIpsConfigContainer   = document.getElementById('relay-ips-config-container');
const settingsLocalIps   = document.getElementById('settings-local-ips');
const settingsPublicIp   = document.getElementById('settings-public-ip');
const settingsLocalSyncPin = document.getElementById('settings-local-sync-pin');
const localSyncPinVal     = document.getElementById('local-sync-pin-val');
const settingsSyncBackupsDir    = document.getElementById('settings-sync-backups-dir');
const settingsAutoDeleteBackups = document.getElementById('settings-auto-delete-backups');
const settingsAutoDeleteDays    = document.getElementById('settings-auto-delete-days');
const autoDeleteDaysContainer   = document.getElementById('auto-delete-days-container');
const settingsCloudEnabled      = document.getElementById('settings-cloud-enabled');
const cloudSyncFields           = document.getElementById('cloud-sync-fields');
const cloudOauthDetails        = document.getElementById('cloud-oauth-details');
const cloudOauthTitle          = document.getElementById('cloud-oauth-title');
const cloudOauthEmail          = document.getElementById('cloud-oauth-email');
const btnCloudConnect          = document.getElementById('btn-cloud-connect');
const btnCloudDisconnect       = document.getElementById('btn-cloud-disconnect');

const cloudFormLocal           = document.getElementById('cloud-form-local');
const cloudFormWebdav          = document.getElementById('cloud-form-webdav');
const cloudFormWebhook         = document.getElementById('cloud-form-webhook');

const settingsCloudLocalDir     = document.getElementById('settings-cloud-local-dir');
const settingsCloudWebdavUrl    = document.getElementById('settings-cloud-webdav-url');
const settingsCloudWebdavUsername = document.getElementById('settings-cloud-webdav-username');
const settingsCloudWebdavPassword = document.getElementById('settings-cloud-webdav-password');
const settingsCloudWebhookUrl   = document.getElementById('settings-cloud-webhook-url');
const settingsCloudWebhookHeaders = document.getElementById('settings-cloud-webhook-headers');

let selectedCloudProvider = 'local';
const pathTranslationsList = document.getElementById('path-translations-list');
const formAddTranslation = document.getElementById('form-add-translation');
const translationFromInput = document.getElementById('translation-from-input');
const translationToInput = document.getElementById('translation-to-input');

const btnBrowseFolder    = document.getElementById('btn-browse-folder');
const settingsAutoSyncOnTrack   = document.getElementById('settings-auto-sync-on-track');
const btnRunScan         = document.getElementById('btn-run-scan');
const btnRunScanInner    = document.getElementById('btn-run-scan-inner');
const btnTrackAll        = document.getElementById('btn-track-all');
const btnUntrackAll      = document.getElementById('btn-untrack-all');

const addGameModal       = document.getElementById('add-game-modal');
const btnAddGameModal    = document.getElementById('btn-add-game-modal');
const btnEmptyAddGame    = document.getElementById('btn-empty-add-game');
const createBranchModal  = document.getElementById('create-branch-modal');
const rollbackConfirmModal = document.getElementById('rollback-confirm-modal');
const snapshotCommentModal = document.getElementById('snapshot-comment-modal');
const cloudExplorerModal = document.getElementById('cloud-explorer-modal');
const btnDrawerCloudExplorer = document.getElementById('btn-drawer-cloud-explorer');
const btnCloseCloudExplorer = document.getElementById('btn-close-cloud-explorer');
const cloudExplorerTableBody = document.getElementById('cloud-explorer-table-body');
const cloudExplorerLoading = document.getElementById('cloud-explorer-loading');
const cloudExplorerEmpty = document.getElementById('cloud-explorer-empty');
const cloudExplorerContent = document.getElementById('cloud-explorer-content');
const btnCloudSyncLocal = document.getElementById('btn-cloud-sync-local');

const gameDetailsDrawer  = document.getElementById('game-details-drawer');
const btnCloseDrawer     = document.getElementById('btn-close-drawer');
const drawerGameName     = document.getElementById('drawer-game-name');
const drawerGamePath     = document.getElementById('drawer-game-path');
const branchSelect       = document.getElementById('branch-select');
const btnCreateBranch    = document.getElementById('btn-create-branch');
const btnDrawerLaunch    = document.getElementById('btn-drawer-launch');
const btnDrawerSync      = document.getElementById('btn-drawer-sync');
const btnDrawerSnapshot  = document.getElementById('btn-drawer-snapshot');
const timelineTree       = document.getElementById('timeline-tree');
const btnDeleteGame      = document.getElementById('btn-delete-game');
const infoBranchName     = document.getElementById('info-branch-name');
const infoSnapshotCount  = document.getElementById('info-snapshot-count');
const infoLastBackup     = document.getElementById('info-last-backup');

const drawerCoverImg     = document.getElementById('drawer-cover-img');
const drawerCoverPlaceholder = document.getElementById('drawer-cover-placeholder');
const formGameLaunchSettings = document.getElementById('form-game-launch-settings');
const drawerGameAppid    = document.getElementById('drawer-game-appid');
const drawerGameExepath  = document.getElementById('drawer-game-exepath');
const btnBrowseGameExe   = document.getElementById('btn-browse-game-exe');
const drawerGameCoverurl = document.getElementById('drawer-game-coverurl');

const customScanPathsList = document.getElementById('custom-scan-paths-list');
const inputNewScanPath = document.getElementById('input-new-scan-path');
const btnBrowseScanPath = document.getElementById('btn-browse-scan-path');
const btnAddScanPath = document.getElementById('btn-add-scan-path');

const drawerSyncProgressContainer = document.getElementById('drawer-sync-progress-container');
const drawerSyncProgressStatus = document.getElementById('drawer-sync-progress-status');
const drawerSyncProgressSpeed = document.getElementById('drawer-sync-progress-speed');
const drawerSyncProgressBar = document.getElementById('drawer-sync-progress-bar');
const drawerSyncProgressDetails = document.getElementById('drawer-sync-progress-details');
const drawerSyncProgressPercent = document.getElementById('drawer-sync-progress-percent');

// ============================================================
// INIT
// ============================================================
function initApp() {
  connectWebSocket();
  setupEventListeners();
  setupNavigation();
  restoreSavedBackupDir();
}

// ============================================================
// SIDEBAR NAVIGATION
// ============================================================
function setupNavigation() {
  const navItems = document.querySelectorAll('.nav-item[data-view]');
  navItems.forEach(item => {
    item.addEventListener('click', () => {
      const viewId = item.getAttribute('data-view');
      navigateTo(viewId);
    });
  });
}

// Tracks whether the devices tab needs to flash
let devicesTabNeedsAttention = false;

function flashDevicesTab() {
  const navPeers = document.getElementById('nav-peers');
  if (!navPeers) return;
  // Don't flash if the user is already on the devices tab
  const currentActive = document.querySelector('.nav-item.active');
  if (currentActive && currentActive.getAttribute('data-view') === 'peers') return;
  devicesTabNeedsAttention = true;
  navPeers.classList.remove('devices-flash');
  // Trigger reflow to restart animation
  void navPeers.offsetWidth;
  navPeers.classList.add('devices-flash');
}

function navigateTo(viewId) {
  // Deactivate all nav items and views
  document.querySelectorAll('.nav-item').forEach(i => i.classList.remove('active'));
  document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));

  // If navigating to peers, clear the flash
  if (viewId === 'peers') {
    devicesTabNeedsAttention = false;
    const navPeers = document.getElementById('nav-peers');
    if (navPeers) navPeers.classList.remove('devices-flash');
  }

  // Activate the clicked nav item and the matching view
  const navItem = document.querySelector(`.nav-item[data-view="${viewId}"]`);
  const view = document.getElementById(`view-${viewId}`);
  if (navItem) navItem.classList.add('active');
  if (view) view.classList.add('active');

  // Side effects on navigation
  if (viewId === 'settings') {
    settingsDeviceName.value = appState.settings.deviceName || '';
    const settingsDeviceType = document.getElementById('settings-device-type');
    if (settingsDeviceType) settingsDeviceType.value = appState.settings.deviceType || 'desktop';
    if (settingsStartOnBoot) settingsStartOnBoot.checked = !!appState.settings.startOnBoot;
    if (settingsSpeedLimit) settingsSpeedLimit.value = appState.settings.speedLimit || '0';
    settingsRelayUrl.value = appState.settings.relayUrl || 'ws://localhost:8386';
    settingsHostRelay.checked = !!appState.settings.hostRelay;
    settingsRelayPort.value = appState.settings.relayPort || 8386;
    if (settingsSyncBackupsDir) settingsSyncBackupsDir.value = appState.settings.syncBackupsDir || '';
    if (settingsAutoDeleteBackups) {
      settingsAutoDeleteBackups.checked = !!appState.settings.autoDeleteBackups;
      toggleAutoDeleteDays(!!appState.settings.autoDeleteBackups);
    }
    if (settingsAutoDeleteDays) settingsAutoDeleteDays.value = appState.settings.autoDeleteDays || '30';
    if (settingsAutoSyncOnTrack) settingsAutoSyncOnTrack.checked = appState.settings.autoSyncOnTrack !== false;
    
    if (settingsCloudEnabled) {
      const cs = appState.settings.cloudSync || {};
      settingsCloudEnabled.checked = !!cs.enabled;
      selectedCloudProvider = cs.provider || 'local';
      
      if (cs.provider === 'local') {
        if (settingsCloudLocalDir) settingsCloudLocalDir.value = cs.url || '';
      } else if (cs.provider === 'webdav') {
        if (settingsCloudWebdavUrl) settingsCloudWebdavUrl.value = cs.url || '';
        if (settingsCloudWebdavUsername) settingsCloudWebdavUsername.value = cs.username || '';
        if (settingsCloudWebdavPassword) settingsCloudWebdavPassword.value = cs.password || '';
      } else if (cs.provider === 'webhook') {
        if (settingsCloudWebhookUrl) settingsCloudWebhookUrl.value = cs.url || '';
        if (settingsCloudWebhookHeaders) settingsCloudWebhookHeaders.value = cs.headers || '{}';
      }

      updateProviderCardsStatuses();
      toggleCloudSyncFields();
    }

    localCustomScanPaths = [...(appState.settings.customScanPaths || [])];
    renderCustomScanPaths();
    localPathTranslations = [...(appState.settings.pathTranslations || [])];
    renderPathTranslations();
    syncWanControls();
    toggleRelayContainers(settingsHostRelay.checked);
    loadRelayIps();
  }
  if (viewId === 'wan') {
    syncWanControls();
    loadRelayIps();
    // Start relay health polling for the WAN view
    loadRelayHealth();
    if (relayHealthTimer) clearInterval(relayHealthTimer);
    relayHealthTimer = setInterval(loadRelayHealth, 15000);
  } else {
    // Stop polling when leaving WAN view
    if (relayHealthTimer) { clearInterval(relayHealthTimer); relayHealthTimer = null; }
  }
}

// ============================================================
// WEBSOCKET
// ============================================================
function connectWebSocket() {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${protocol}//${window.location.host}`;
  showToast('Connecting to OpenSave daemon...', 'info');

  ws = new WebSocket(wsUrl);

  ws.onopen = () => {
    titlebarStatus.classList.remove('offline');
    daemonStatusText.textContent = 'Daemon Online';
    showToast('Daemon connected! Real-time syncing active.', 'success');
  };

  ws.onmessage = (event) => {
    try {
      const message = JSON.parse(event.data);
      handleWebSocketMessage(message);
    } catch (e) {
      console.error('Error parsing WS message:', e);
    }
  };

  ws.onclose = () => {
    titlebarStatus.classList.add('offline');
    daemonStatusText.textContent = 'Daemon Offline';
    showToast('Daemon connection lost. Reconnecting...', 'error');
    setTimeout(connectWebSocket, 3000);
  };
}

function handleWebSocketMessage(message) {
  const { event, data } = message;
  switch (event) {
    case 'init':
      appState = data;
      renderAll();
      checkActiveConflicts(data.activeConflicts);
      initConsole(data.logHistory);
      break;
    case 'console-log':
      appendLogLine(data);
      break;
    case 'games-update':
      appState.games = data;
      renderGames();
      updateStats();
      if (activeGameId) renderDrawerDetails();
      if (discoveredSavesList.length > 0) renderScanResults(discoveredSavesList);
      break;
    case 'peers-update':
      appState.peers = data.paired;
      appState.discoveredPeers = data.discovered;
      appState.pairingRequests = data.requests;
      appState.wanRoom = data.wanRoom || appState.wanRoom;
      renderPeers();
      renderWanRoom();
      updateStats();
      updateConsoleDevices();
      // Flash devices tab if there are pending pairing requests
      if ((data.requests || []).length > 0) {
        flashDevicesTab();
      }
      break;
    case 'sync-start':
      statSyncStatus.textContent = 'Syncing...';
      statSyncStatus.className = 'stat-pill-val text-warning';
      showToast(data.message || 'Syncing saves...', 'info');
      updateConsoleDevices();
      flashDevicesTab();
      if (activeGameId === data.gameId) {
        if (btnDrawerSync) {
          btnDrawerSync.disabled = true;
          btnDrawerSync.textContent = '⚡ Syncing...';
        }
        if (drawerSyncProgressContainer) {
          drawerSyncProgressContainer.classList.remove('hidden');
          drawerSyncProgressStatus.textContent = 'Connecting to peer...';
          drawerSyncProgressSpeed.textContent = '0 KB/s';
          drawerSyncProgressBar.style.width = '0%';
          drawerSyncProgressDetails.textContent = 'Calculating...';
          drawerSyncProgressPercent.textContent = '0%';
        }
      }
      break;
    case 'sync-progress':
      const speedStr = (data.speedBytesPerSec > 1024 * 1024) 
        ? `${(data.speedBytesPerSec / 1024 / 1024).toFixed(1)} MB/s` 
        : `${(data.speedBytesPerSec / 1024).toFixed(1)} KB/s`;
      statSyncStatus.textContent = `Syncing (${data.percentage}%)`;
      if (activeGameId === data.gameId) {
        if (btnDrawerSync) {
          btnDrawerSync.disabled = true;
          btnDrawerSync.textContent = `⚡ Syncing (${data.percentage}%)`;
        }
        if (drawerSyncProgressContainer) {
          drawerSyncProgressContainer.classList.remove('hidden');
          drawerSyncProgressStatus.textContent = `Syncing with ${data.peerName}...`;
          drawerSyncProgressSpeed.textContent = speedStr;
          drawerSyncProgressBar.style.width = `${data.percentage}%`;
          drawerSyncProgressDetails.textContent = `${(data.bytesTransferred / 1024 / 1024).toFixed(2)} MB / ${(data.totalBytes / 1024 / 1024).toFixed(2)} MB`;
          drawerSyncProgressPercent.textContent = `${data.percentage}%`;
        }
      }
      break;
    case 'sync-complete':
      statSyncStatus.textContent = 'Idle';
      statSyncStatus.className = 'stat-pill-val text-success';
      if (btnDrawerSync) {
        btnDrawerSync.disabled = false;
        btnDrawerSync.textContent = '⚡ Sync Now';
      }
      if (drawerSyncProgressContainer) {
        drawerSyncProgressContainer.classList.add('hidden');
      }
      let conflictPeer = null;
      if (data.result && data.result.peersSynced) {
        conflictPeer = data.result.peersSynced.find(p => p.status === 'conflict');
      }
      if (conflictPeer) {
        showToast(`Sync conflict detected for ${getGameName(data.gameId)}!`, 'warning');
        openConflictModal(data.gameId, conflictPeer.peerId, conflictPeer.peerName, conflictPeer.localSnap, conflictPeer.remoteSnap);
      } else {
        showToast('Sync complete!', 'success');
      }
      updateConsoleDevices();
      break;
    case 'sync-error':
      statSyncStatus.textContent = 'Error';
      statSyncStatus.className = 'stat-pill-val text-danger';
      if (btnDrawerSync) {
        btnDrawerSync.disabled = false;
        btnDrawerSync.textContent = '⚡ Sync Now';
      }
      if (drawerSyncProgressContainer) {
        drawerSyncProgressContainer.classList.add('hidden');
      }
      showToast(`Sync failed: ${data.error}`, 'error');
      updateConsoleDevices();
      break;
  }
}

function getGameName(gameId) {
  return appState.games[gameId]?.name || gameId;
}

// ============================================================
// EVENT LISTENERS
// ============================================================
function setupEventListeners() {
  // Custom Titlebar Window Controls
  document.getElementById('titlebar-minimize')?.addEventListener('click', () => {
    fetch('/api/window/minimize', { method: 'POST' }).catch(() => {});
  });
  document.getElementById('titlebar-maximize')?.addEventListener('click', () => {
    fetch('/api/window/maximize', { method: 'POST' }).catch(() => {});
  });
  document.getElementById('titlebar-close')?.addEventListener('click', () => {
    fetch('/api/window/close', { method: 'POST' }).catch(() => {});
  });

  // Add Game Modal
  btnAddGameModal.addEventListener('click', () => openModal(addGameModal));
  btnEmptyAddGame?.addEventListener('click', () => openModal(addGameModal));
  btnCreateBranch.addEventListener('click', () => openModal(createBranchModal));

  // Close Modals
  document.querySelectorAll('.btn-close-modal').forEach(btn => {
    btn.addEventListener('click', () => closeModal(addGameModal));
  });
  document.querySelectorAll('.btn-close-submodal').forEach(btn => {
    btn.addEventListener('click', () => {
      closeModal(createBranchModal);
      closeModal(rollbackConfirmModal);
      closeModal(snapshotCommentModal);
      const bModal = document.getElementById('snapshot-browser-modal');
      if (bModal) closeModal(bModal);
    });
  });

  // Cloud Backup Explorer Modal
  btnDrawerCloudExplorer?.addEventListener('click', () => {
    const hasCloud = appState.settings.cloudSync?.enabled;
    if (!hasCloud) {
      showToast('Cloud Backup is not enabled. Configure it in Settings first.', 'warning');
      return;
    }
    openCloudExplorer(activeGameId);
  });

  btnCloseCloudExplorer?.addEventListener('click', () => {
    closeModal(cloudExplorerModal);
  });

  btnCloudSyncLocal?.addEventListener('click', async () => {
    if (!activeGameId) return;
    
    btnCloudSyncLocal.disabled = true;
    const origText = btnCloudSyncLocal.textContent;
    btnCloudSyncLocal.textContent = 'Syncing...';

    try {
      const res = await fetch(`/api/cloud/sync-local/${activeGameId}`, { method: 'POST' });
      if (res.ok) {
        const data = await res.json();
        showToast(`Successfully uploaded ${data.uploaded} snapshots to the cloud!`, 'success');
        openCloudExplorer(activeGameId);
      } else {
        const err = await res.json();
        showToast(`Sync failed: ${err.error}`, 'error');
      }
    } catch (err) {
      showToast(`Error: ${err.message}`, 'error');
    } finally {
      btnCloudSyncLocal.disabled = false;
      btnCloudSyncLocal.textContent = origText;
    }
  });

  // Browse Folder (Add Game)
  btnBrowseFolder.addEventListener('click', async () => {
    btnBrowseFolder.disabled = true;
    const orig = btnBrowseFolder.textContent;
    btnBrowseFolder.textContent = '...';
    try {
      const res = await fetch('/api/browse-directory');
      if (res.ok) {
        const data = await res.json();
        if (data.path) gamePathInput.value = data.path;
      } else {
        const err = await res.json();
        showToast(err.error || 'Browse folder only available in the desktop app.', 'warning');
      }
    } catch (err) {
      showToast('Error: ' + err.message, 'error');
    } finally {
      btnBrowseFolder.disabled = false;
      btnBrowseFolder.textContent = orig;
    }
  });

  // Add Game Submit
  formAddGame.addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = gameNameInput.value.trim();
    const savePath = gamePathInput.value.trim();
    await trackFolder(name, savePath);
    formAddGame.reset();
    closeModal(addGameModal);
  });


  // Settings tab switching
  document.querySelectorAll('.settings-tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.settings-tab-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      const targetTab = btn.getAttribute('data-settings-tab');
      document.querySelectorAll('.settings-tab-content').forEach(content => {
        content.classList.toggle('hidden', content.id !== `settings-tab-${targetTab}`);
      });
    });
  });

  // WAN Sync tab switching
  document.querySelectorAll('.wan-tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.wan-tab-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      const targetTab = btn.getAttribute('data-wan-tab');
      document.querySelectorAll('.wan-tab-content').forEach(content => {
        content.classList.toggle('hidden', content.id !== `wan-tab-${targetTab}`);
      });
    });
  });

  // Settings Submit
  formUpdateSettings.addEventListener('submit', async (e) => {
    e.preventDefault();
    const deviceName = settingsDeviceName.value.trim();
    const deviceType = document.getElementById('settings-device-type')?.value || 'desktop';
    const startOnBoot = settingsStartOnBoot ? settingsStartOnBoot.checked : false;
    const speedLimit  = settingsSpeedLimit ? parseInt(settingsSpeedLimit.value, 10) : 0;
    const relayUrl   = settingsRelayUrl.value.trim();
    const hostRelay  = settingsHostRelay.checked;
    const relayPort  = parseInt(settingsRelayPort.value, 10) || 8386;
    const syncBackupsDir = settingsSyncBackupsDir ? settingsSyncBackupsDir.value.trim() : '';
    const autoDeleteBackups = settingsAutoDeleteBackups ? settingsAutoDeleteBackups.checked : false;
    const autoDeleteDays = settingsAutoDeleteDays ? parseInt(settingsAutoDeleteDays.value, 10) || 30 : 30;
    const autoSyncOnTrack = settingsAutoSyncOnTrack ? settingsAutoSyncOnTrack.checked : true;

    // Collect current custom client ID for this provider (if OAuth)
    const isOAuthProvider = ['google_drive', 'onedrive', 'dropbox'].includes(selectedCloudProvider);
    const existingCustomIds = appState.settings.cloudSync?.customClientIds || {};
    const customClientIdInput = document.getElementById('settings-cloud-client-id');
    const currentCustomClientId = (isOAuthProvider && customClientIdInput) ? customClientIdInput.value.trim() : '';
    const mergedCustomIds = {
      ...existingCustomIds,
      ...(isOAuthProvider ? { [selectedCloudProvider]: currentCustomClientId } : {})
    };

    const cloudSync = {
      enabled: settingsCloudEnabled ? settingsCloudEnabled.checked : false,
      provider: selectedCloudProvider,
      url: selectedCloudProvider === 'local'
        ? (settingsCloudLocalDir ? settingsCloudLocalDir.value.trim() : '')
        : selectedCloudProvider === 'webdav'
          ? (settingsCloudWebdavUrl ? settingsCloudWebdavUrl.value.trim() : '')
          : selectedCloudProvider === 'webhook'
            ? (settingsCloudWebhookUrl ? settingsCloudWebhookUrl.value.trim() : '')
            : '',
      username: selectedCloudProvider === 'webdav' && settingsCloudWebdavUsername ? settingsCloudWebdavUsername.value.trim() : '',
      password: selectedCloudProvider === 'webdav' && settingsCloudWebdavPassword ? settingsCloudWebdavPassword.value : '',
      headers: selectedCloudProvider === 'webhook' && settingsCloudWebhookHeaders ? settingsCloudWebhookHeaders.value.trim() : '{}',
      customClientIds: mergedCustomIds,
      tokens: appState.settings.cloudSync?.tokens || {}
    };

    const uiMode = 'modern';

    await saveSettings({
      deviceName,
      deviceType,
      uiMode,
      relayUrl,
      syncCode: appState.settings.syncCode,
      hostRelay,
      relayPort,
      startOnBoot,
      speedLimit,
      syncBackupsDir,
      autoDeleteBackups,
      autoDeleteDays,
      autoSyncOnTrack,
      customScanPaths: localCustomScanPaths,
      pathTranslations: localPathTranslations,
      cloudSync
    });
    await loadRelayIps();
  });

  // Host Relay Toggle
  settingsHostRelay.addEventListener('change', () => toggleRelayContainers(settingsHostRelay.checked));

  // Auto-Delete Backup Toggle
  settingsAutoDeleteBackups?.addEventListener('change', () => toggleAutoDeleteDays(settingsAutoDeleteBackups.checked));

  // Cloud Sync Toggle
  settingsCloudEnabled?.addEventListener('change', () => toggleCloudSyncFields());

  // Cloud Storage Provider Card Click — OAuth cards immediately launch sign-in,
  // already-connected OAuth cards do nothing, non-OAuth cards just select the provider.
  document.querySelectorAll('.provider-card').forEach(card => {
    card.addEventListener('click', () => {
      const provider = card.getAttribute('data-provider');
      if (!provider) return;

      const isOAuth = ['google_drive', 'onedrive', 'dropbox'].includes(provider);
      if (!isOAuth) {
        // Local / WebDAV / Webhook — just select
        selectCloudProvider(provider);
        return;
      }

      // OAuth card — if already connected, do nothing
      const cs = appState.settings.cloudSync || {};
      if (cs.provider === provider && cs.tokens?.userEmail && cs.enabled) {
        return; // already connected, inert
      }

      // Not yet connected — trigger sign-in immediately
      selectCloudProvider(provider);
      triggerOAuthConnect(provider);
    });
  });

  // Cloud Connect / Disconnect Buttons
  // The connect button is now kept as a fallback for keyboard/accessibility users.
  document.getElementById('btn-cloud-connect')?.addEventListener('click', () => {
    triggerOAuthConnect(selectedCloudProvider);
  });

  document.getElementById('btn-cloud-disconnect')?.addEventListener('click', async () => {
    const origText = btnCloudDisconnect.textContent;
    btnCloudDisconnect.disabled = true;
    btnCloudDisconnect.textContent = 'Disconnecting...';
    try {
      const res = await fetch('/api/auth/disconnect', { method: 'POST' });
      if (!res.ok) {
        throw new Error('Failed to disconnect service.');
      }
      showToast(`Disconnected ${selectedCloudProvider.replace('_', ' ')} successfully.`, 'info');
      
      // Update local state immediately
      appState.settings.cloudSync = {
        ...appState.settings.cloudSync,
        enabled: false,
        tokens: {
          accessToken: '',
          refreshToken: '',
          expiryTime: 0,
          userEmail: ''
        }
      };
      updateProviderCardsStatuses();
      selectCloudProvider(selectedCloudProvider);
    } catch (err) {
      showToast(err.message, 'error');
    } finally {
      btnCloudDisconnect.disabled = false;
      btnCloudDisconnect.textContent = origText;
    }
  });

  // Browse Button for local cloud backup directory
  document.getElementById('btn-browse-cloud-local-dir')?.addEventListener('click', async () => {
    try {
      const res = await fetch('/api/browse-directory');
      if (res.ok) {
        const data = await res.json();
        if (data.path && settingsCloudLocalDir) {
          settingsCloudLocalDir.value = data.path;
        }
      }
    } catch (e) {}
  });

  // Browse for Sync Backup Dir
  document.getElementById('btn-browse-sync-backups-dir')?.addEventListener('click', async () => {
    try {
      const res = await fetch('/api/browse-directory');
      if (res.ok) {
        const data = await res.json();
        if (data.path && settingsSyncBackupsDir) {
          settingsSyncBackupsDir.value = data.path;
        }
      }
    } catch (e) {}
  });


  // Custom scan path bindings
  btnBrowseScanPath?.addEventListener('click', async () => {
    try {
      const res = await fetch('/api/browse-directory');
      if (res.ok) {
        const data = await res.json();
        if (data.path && inputNewScanPath) {
          inputNewScanPath.value = data.path;
        }
      }
    } catch (e) {}
  });

  btnAddScanPath?.addEventListener('click', () => {
    if (!inputNewScanPath) return;
    const p = inputNewScanPath.value.trim();
    if (p) {
      if (!localCustomScanPaths.includes(p)) {
        localCustomScanPaths.push(p);
        renderCustomScanPaths();
      }
      inputNewScanPath.value = '';
    }
  });

  formAddTranslation?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const fromPattern = translationFromInput.value.trim();
    const toPattern = translationToInput.value.trim();
    if (!fromPattern || !toPattern) return;

    const exists = localPathTranslations.some(
      r => r.fromPattern.toLowerCase() === fromPattern.toLowerCase() && r.toPattern.toLowerCase() === toPattern.toLowerCase()
    );
    if (exists) {
      showToast('This translation rule already exists.', 'error');
      return;
    }

    localPathTranslations.push({ fromPattern, toPattern });
    renderPathTranslations();

    await saveSettings({ pathTranslations: localPathTranslations }, '🔄 Path translation rule added!');

    translationFromInput.value = '';
    translationToInput.value = '';
  });

  // Scanner
  btnRunScan.addEventListener('click', runDirectoryScan);
  btnRunScanInner?.addEventListener('click', runDirectoryScan);

  // Track All
  btnTrackAll.addEventListener('click', async () => {
    const untracked = discoveredSavesList.filter(item =>
      !Object.values(appState.games).some(g => g.savePath.toLowerCase() === item.savePath.toLowerCase())
    );
    if (untracked.length === 0) return;
    btnTrackAll.disabled = true;
    btnTrackAll.textContent = 'Tracking...';
    showToast(`Tracking ${untracked.length} save folders...`, 'info');
    let count = 0;
    for (const item of untracked) {
      const success = await trackFolder(item.name, item.savePath, item.appId || null);
      if (success) count++;
    }
    showToast(`Tracked ${count} / ${untracked.length} folders!`, 'success');
    btnTrackAll.disabled = false;
    btnTrackAll.textContent = '➕ Track All';
    runDirectoryScan();
  });

  // Untrack All
  btnUntrackAll.addEventListener('click', async () => {
    const tracked = discoveredSavesList.filter(item =>
      Object.values(appState.games).some(g => g.savePath.toLowerCase() === item.savePath.toLowerCase())
    );
    if (tracked.length === 0) return;
    if (!confirm(`Stop tracking all ${tracked.length} autodetected save folders?`)) return;
    btnUntrackAll.disabled = true;
    btnUntrackAll.textContent = 'Untracking...';
    let count = 0;
    for (const item of tracked) {
      const matchedGame = Object.values(appState.games).find(g => g.savePath.toLowerCase() === item.savePath.toLowerCase());
      if (matchedGame) {
        try {
          const res = await fetch(`/api/games/${matchedGame.id}`, { method: 'DELETE' });
          if (res.ok) count++;
        } catch (e) {}
      }
    }
    showToast(`Untracked ${count} folders.`, 'info');
    btnUntrackAll.disabled = false;
    btnUntrackAll.textContent = '❌ Untrack All';
    runDirectoryScan();
  });

  // WAN Sync
  formWanSync.addEventListener('submit', async (e) => {
    e.preventDefault();
    await updateSyncCode(wanCodeInput.value.trim());
  });
  btnLeaveWanRoom.addEventListener('click', async () => await updateSyncCode(''));
  btnGenerateWanCode.addEventListener('click', () => {
    const chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
    let code = 'ss-';
    for (let i = 0; i < 8; i++) code += chars[Math.floor(Math.random() * chars.length)];
    wanCodeInput.value = code;
    updateSyncCode(code);
  });
  btnSaveWanHosting?.addEventListener('click', async () => {
    await saveSettings({
      relayUrl: wanRelayUrlInput?.value.trim() || appState.settings.relayUrl,
      hostRelay: !!wanHostRelay?.checked,
      relayPort: parseInt(wanRelayPortInput?.value, 10) || 8386
    }, 'WAN relay settings saved.');
    await loadRelayIps();
  });
  btnUseLocalRelay?.addEventListener('click', async () => {
    const port = parseInt(wanRelayPortInput?.value, 10) || 8386;
    if (wanRelayUrlInput) wanRelayUrlInput.value = `ws://localhost:${port}`;
    if (wanHostRelay) wanHostRelay.checked = true;
    await saveSettings({
      relayUrl: `ws://localhost:${port}`,
      hostRelay: true,
      relayPort: port
    }, 'Local relay enabled.');
    await loadRelayIps();
  });

  // "Use Cloud Relay" button — sets relay URL to the public OpenSave relay
  document.getElementById('btn-use-cloud-relay')?.addEventListener('click', async () => {
    if (wanRelayUrlInput) wanRelayUrlInput.value = CLOUD_RELAY_URL;
    if (wanHostRelay) wanHostRelay.checked = false;
    await saveSettings({
      relayUrl: CLOUD_RELAY_URL,
      hostRelay: false
    }, '☁️ Switched to OpenSave cloud relay!');
    loadRelayHealth();
  });

  // Manual Add Peer
  formAddPeer.addEventListener('submit', async (e) => {
    e.preventDefault();
    let address = peerAddressInput.value.trim();
    let port = parseInt(peerPortInput.value.trim(), 10);
    if (isNaN(port)) port = 8383;

    if (address.toUpperCase().startsWith('SS-LAN-') || /^[A-Z0-9]{6}$/i.test(address)) {
      let pin = address;
      if (!pin.toUpperCase().startsWith('SS-LAN-')) {
        pin = 'SS-LAN-' + pin;
      }
      const decoded = decodePin(pin);
      if (decoded) {
        address = decoded.ip;
        port = decoded.port;
      } else {
        showToast('Invalid PIN Code format.', 'error');
        return;
      }
    }

    try {
      const probe = await probePeerAddress(address, port);
      if (!probe) return;

      showToast(`Sending pairing request to ${probe.deviceName} at ${address}:${port}...`, 'info');
      const res = await fetch('/api/peers/pair', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address, port })
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Pairing: ${data.message || 'Request sent!'}`, 'success');
      } else {
        showToast(data.error, 'error');
      }
    } catch (err) {
      showToast(`Pairing failed: ${err.message}`, 'error');
    }
  });

  const probeOnInput = debounce(async () => {
    let address = peerAddressInput.value.trim();
    let port = parseInt(peerPortInput.value.trim(), 10);
    if (isNaN(port)) port = 8383;

    if (!address) {
      setPeerProbeStatus('', '');
      return;
    }

    if (address.toUpperCase().startsWith('SS-LAN-') || /^[A-Z0-9]{6}$/i.test(address)) {
      let pin = address;
      if (!pin.toUpperCase().startsWith('SS-LAN-')) {
        pin = 'SS-LAN-' + pin;
      }
      const decoded = decodePin(pin);
      if (decoded) {
        address = decoded.ip;
        port = decoded.port;
      } else {
        setPeerProbeStatus('Invalid PIN Code', 'error');
        return;
      }
    }

    await probePeerAddress(address, port, { quiet: true });
  }, 700);

  peerAddressInput.addEventListener('input', probeOnInput);
  peerPortInput.addEventListener('input', probeOnInput);

  // Drawer
  btnCloseDrawer.addEventListener('click', closeDrawer);
  branchSelect.addEventListener('change', async (e) => {
    const branchName = e.target.value;
    const res = await fetch(`/api/games/${activeGameId}/branch/switch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ branchName })
    });
    if (res.ok) showToast(`Switched to branch: ${branchName}`, 'success');
    else { const err = await res.json(); showToast(err.error, 'error'); }
  });

  formCreateBranch.addEventListener('submit', async (e) => {
    e.preventDefault();
    const branchName = branchNameInput.value.trim();
    const res = await fetch(`/api/games/${activeGameId}/branch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ branchName })
    });
    if (res.ok) {
      showToast(`Branch "${branchName}" created!`, 'success');
      formCreateBranch.reset();
      closeModal(createBranchModal);
    } else {
      const err = await res.json(); showToast(err.error, 'error');
    }
  });

  btnDrawerSnapshot.addEventListener('click', () => openModal(snapshotCommentModal));
  formSnapshotComment.addEventListener('submit', async (e) => {
    e.preventDefault();
    const comment = snapshotCommentInput.value.trim();
    const res = await fetch(`/api/games/${activeGameId}/snapshot`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ comment })
    });
    if (res.ok) {
      showToast('Snapshot saved!', 'success');
      formSnapshotComment.reset();
      closeModal(snapshotCommentModal);
    } else {
      const err = await res.json(); showToast(err.error, 'error');
    }
  });

  btnDrawerSync.addEventListener('click', async () => {
    const res = await fetch(`/api/games/${activeGameId}/sync`, { method: 'POST' });
    if (!res.ok) { const err = await res.json(); showToast(`Sync failed: ${err.error}`, 'error'); }
  });

  document.getElementById('btn-confirm-rollback-execute').addEventListener('click', async () => {
    if (!activeGameId || !pendingRollbackSnapId) return;
    const res = await fetch(`/api/games/${activeGameId}/rollback`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ snapshotId: pendingRollbackSnapId })
    });
    if (res.ok) {
      showToast(`Rolled back to: ${pendingRollbackSnapId}`, 'success');
      closeModal(rollbackConfirmModal);
      pendingRollbackSnapId = null;
    } else {
      const err = await res.json(); showToast(err.error, 'error');
    }
  });

  btnDeleteGame.addEventListener('click', async () => {
    if (!confirm('Stop tracking this folder? Snapshots will not be deleted.')) return;
    const res = await fetch(`/api/games/${activeGameId}`, { method: 'DELETE' });
    if (res.ok) { showToast('Game untracked.', 'info'); closeDrawer(); }
    else { const err = await res.json(); showToast(err.error, 'error'); }
  });

  // Browse Executable (Game Launch Settings)
  btnBrowseGameExe?.addEventListener('click', async () => {
    btnBrowseGameExe.disabled = true;
    const orig = btnBrowseGameExe.textContent;
    btnBrowseGameExe.textContent = '...';
    try {
      const res = await fetch('/api/browse-file');
      if (res.ok) {
        const data = await res.json();
        if (data.path) drawerGameExepath.value = data.path;
      } else {
        const err = await res.json();
        showToast(err.error || 'Browse file only available in the desktop app.', 'warning');
      }
    } catch (err) {
      showToast('Error: ' + err.message, 'error');
    } finally {
      btnBrowseGameExe.disabled = false;
      btnBrowseGameExe.textContent = orig;
    }
  });

  // Save Game Launch Config
  formGameLaunchSettings?.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!activeGameId) return;

    const appId = drawerGameAppid.value.trim();
    const exePath = drawerGameExepath.value.trim();
    const coverUrl = drawerGameCoverurl.value.trim();
    const drawerGameAutosync = document.getElementById('drawer-game-autosync');
    const drawerGameMaxsnapshots = document.getElementById('drawer-game-maxsnapshots');
    const autoSync = drawerGameAutosync ? drawerGameAutosync.checked : true;
    const maxSnapshots = drawerGameMaxsnapshots ? parseInt(drawerGameMaxsnapshots.value, 10) || 0 : 5;

    try {
      const res = await fetch(`/api/games/${activeGameId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ appId, exePath, coverUrl, autoSync, maxSnapshots })
      });
      if (res.ok) {
        showToast('Launch configuration updated!', 'success');
        const updatedGame = await res.json();
        appState.games[activeGameId] = updatedGame;
        renderGames();
        renderDrawerDetails();
      } else {
        const err = await res.json();
        showToast(err.error || 'Failed to save configuration', 'error');
      }
    } catch (err) {
      showToast('Error: ' + err.message, 'error');
    }
  });

  // Launch Game Action
  btnDrawerLaunch?.addEventListener('click', async () => {
    if (!activeGameId) return;
    btnDrawerLaunch.disabled = true;
    const origText = btnDrawerLaunch.textContent;
    btnDrawerLaunch.textContent = 'Launching...';
    try {
      const res = await fetch(`/api/games/${activeGameId}/launch`, { method: 'POST' });
      if (res.ok) {
        showToast('Game launch requested.', 'success');
      } else {
        const err = await res.json();
        showToast(err.error || 'Launch failed', 'error');
      }
    } catch (err) {
      showToast('Launch error: ' + err.message, 'error');
    } finally {
      btnDrawerLaunch.disabled = false;
      btnDrawerLaunch.textContent = origText;
    }
  });

  // Conflict Resolution
  document.getElementById('btn-conflict-keep-local')?.addEventListener('click', () => resolveActiveConflict('keep-local'));
  document.getElementById('btn-conflict-keep-remote')?.addEventListener('click', () => resolveActiveConflict('keep-remote'));
  document.getElementById('btn-conflict-keep-both')?.addEventListener('click', () => resolveActiveConflict('merge-branch'));
  document.getElementById('btn-close-conflict-modal')?.addEventListener('click', () => {
    document.getElementById('conflict-modal').classList.add('hidden');
    activeConflictData = null;
  });

  // ---- BACKUP EXPORT ----
  const btnBrowseBackupDir = document.getElementById('btn-browse-backup-dir');
  const inputBackupDir     = document.getElementById('settings-backup-dir');
  const btnExecuteBackup   = document.getElementById('btn-execute-backup');
  const backupResultsContainer = document.getElementById('backup-results-container');
  const lblBackupFolder    = document.getElementById('lbl-backup-folder');
  const lblBackupRatio     = document.getElementById('lbl-backup-ratio');
  const lblBackupOrig      = document.getElementById('lbl-backup-orig');
  const lblBackupComp      = document.getElementById('lbl-backup-comp');

  btnBrowseBackupDir?.addEventListener('click', () => browseDirectory(inputBackupDir, 'opensave_last_backup_dir'));

  btnExecuteBackup?.addEventListener('click', async () => {
    const exportDir = inputBackupDir?.value.trim();
    if (!exportDir) { showToast('Please specify a destination directory first.', 'warning'); return; }
    btnExecuteBackup.disabled = true;
    const origText = btnExecuteBackup.textContent;
    btnExecuteBackup.textContent = '📦 Compressing...';
    backupResultsContainer?.classList.add('hidden');
    try {
      showToast('Starting Brotli backup...', 'info');
      const res = await fetch('/api/backup/export', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ exportDir })
      });
      if (res.ok) {
        const data = await res.json();
        localStorage.setItem('opensave_last_backup_dir', exportDir);
        if (lblBackupFolder) lblBackupFolder.textContent = data.backupFolder;
        if (lblBackupRatio)  lblBackupRatio.textContent  = data.savings;
        if (lblBackupOrig)   lblBackupOrig.textContent   = (data.totalOriginal / 1024).toFixed(1) + ' KB';
        if (lblBackupComp)   lblBackupComp.textContent   = (data.totalCompressed / 1024).toFixed(1) + ' KB';
        backupResultsContainer?.classList.remove('hidden');
        showToast('Backup created successfully!', 'success');
      } else {
        const err = await res.json();
        showToast(err.error || 'Backup failed', 'error');
      }
    } catch (err) {
      showToast('Backup error: ' + err.message, 'error');
    } finally {
      btnExecuteBackup.disabled = false;
      btnExecuteBackup.textContent = origText;
    }
  });

  // ---- BACKUP RESTORE ----
  const btnBrowseRestoreDir    = document.getElementById('btn-browse-restore-dir');
  const inputRestoreDir        = document.getElementById('restore-backup-dir');
  const btnExecuteRestore      = document.getElementById('btn-execute-restore');
  const restoreResultsContainer= document.getElementById('restore-results-container');
  const restoreResultTitle     = document.getElementById('restore-result-title');
  const restoreResultDetails   = document.getElementById('restore-result-details');

  btnBrowseRestoreDir?.addEventListener('click', () => browseDirectory(inputRestoreDir, 'opensave_last_restore_dir'));

  btnExecuteRestore?.addEventListener('click', async () => {
    const backupPath = inputRestoreDir?.value.trim();
    if (!backupPath) { showToast('Please select a backup folder first.', 'warning'); return; }

    btnExecuteRestore.disabled = true;
    const origText = btnExecuteRestore.textContent;
    btnExecuteRestore.textContent = '🔄 Restoring...';
    restoreResultsContainer?.classList.add('hidden');

    try {
      showToast('Restoring saves from backup...', 'info');
      const res = await fetch('/api/backup/restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ backupPath })
      });

      if (res.ok) {
        const data = await res.json();
        if (restoreResultTitle) {
          restoreResultTitle.textContent = `✓ Restore Complete: ${data.restored} restored, ${data.skipped} skipped, ${data.errors} errors`;
        }
        if (restoreResultDetails) {
          restoreResultDetails.innerHTML = '';
          data.details.forEach(d => {
            const icon = d.status === 'restored' ? '✅' : d.status === 'skipped' ? '⏭️' : '❌';
            const div = document.createElement('div');
            div.className = 'restore-detail-item';
            div.innerHTML = `<span>${icon}</span><strong>${d.name}</strong><span style="color:var(--text-3)">${d.reason || d.path || ''}</span>`;
            restoreResultDetails.appendChild(div);
          });
        }
        restoreResultsContainer?.classList.remove('hidden');
        showToast(`Restore complete! ${data.restored} game(s) restored.`, 'success');
      } else {
        const err = await res.json();
        showToast(err.error || 'Restore failed', 'error');
      }
    } catch (err) {
      showToast('Restore error: ' + err.message, 'error');
    } finally {
      btnExecuteRestore.disabled = false;
      btnExecuteRestore.textContent = origText;
    }
  });
}

function debounce(fn, delay) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), delay);
  };
}

function setPeerProbeStatus(message, type = 'info') {
  if (!peerProbeStatus) return;
  if (!message) {
    peerProbeStatus.classList.add('hidden');
    peerProbeStatus.textContent = '';
    return;
  }
  peerProbeStatus.classList.remove('hidden');
  peerProbeStatus.textContent = message;
  peerProbeStatus.style.borderColor = type === 'success' ? 'rgba(16,185,129,0.35)' : type === 'error' ? 'rgba(239,68,68,0.35)' : '';
  peerProbeStatus.style.color = type === 'success' ? 'var(--emerald)' : type === 'error' ? 'var(--red)' : '';
}

async function probePeerAddress(address, port, { quiet = false } = {}) {
  try {
    setPeerProbeStatus(`Checking ${address}:${port}...`, 'info');
    const res = await fetch('/api/peers/probe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address, port })
    });
    const data = await res.json();
    if (!res.ok || !data.reachable) {
      const message = data.error || `Could not reach OpenSave at ${address}:${port}.`;
      setPeerProbeStatus(message, 'error');
      if (!quiet) showToast(message, 'error');
      return null;
    }

    setPeerProbeStatus(`Found ${data.deviceName} (${data.deviceType}) at ${address}:${port}.`, 'success');
    return data;
  } catch (err) {
    const message = `Could not check ${address}:${port}: ${err.message}`;
    setPeerProbeStatus(message, 'error');
    if (!quiet) showToast(message, 'error');
    return null;
  }
}

// ============================================================
// HELPERS
// ============================================================
async function browseDirectory(inputEl, storageKey) {
  const btn = document.activeElement;
  if (btn) btn.disabled = true;
  try {
    const res = await fetch('/api/browse-directory');
    if (res.ok) {
      const data = await res.json();
      if (data.path) {
        inputEl.value = data.path;
        if (storageKey) localStorage.setItem(storageKey, data.path);
      }
    } else {
      const err = await res.json();
      showToast(err.error || 'Browse only available in the desktop app.', 'warning');
    }
  } catch (err) {
    showToast('Error: ' + err.message, 'error');
  } finally {
    if (btn) btn.disabled = false;
  }
}

function restoreSavedBackupDir() {
  const backupInput = document.getElementById('settings-backup-dir');
  const restoreInput = document.getElementById('restore-backup-dir');
  const savedBackup = localStorage.getItem('opensave_last_backup_dir');
  const savedRestore = localStorage.getItem('opensave_last_restore_dir');
  if (backupInput && savedBackup) backupInput.value = savedBackup;
  if (restoreInput && savedRestore) restoreInput.value = savedRestore;
}

async function trackFolder(name, savePath, appId = null) {
  try {
    const body = { name, savePath };
    if (appId) body.appId = String(appId);
    const res = await fetch('/api/games', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    if (res.ok) { showToast(`Started monitoring: ${name}`, 'success'); return true; }
    else { const err = await res.json(); showToast(err.error, 'error'); return false; }
  } catch (err) { showToast(`Failed: ${err.message}`, 'error'); return false; }
}

function syncWanControls() {
  if (wanRelayUrlInput) wanRelayUrlInput.value = appState.settings.relayUrl || 'ws://localhost:8386';
  if (wanHostRelay) wanHostRelay.checked = !!appState.settings.hostRelay;
  if (wanRelayPortInput) wanRelayPortInput.value = appState.settings.relayPort || 8386;
}

async function saveSettings(fields, successMessage = 'Settings saved!') {
  try {
    const payload = {
      deviceName: appState.settings.deviceName,
      deviceType: appState.settings.deviceType || 'desktop',
      relayUrl: appState.settings.relayUrl || 'ws://localhost:8386',
      syncCode: appState.settings.syncCode || '',
      hostRelay: !!appState.settings.hostRelay,
      relayPort: appState.settings.relayPort || 8386,
      startOnBoot: !!appState.settings.startOnBoot,
      speedLimit: appState.settings.speedLimit || 0,
      ...fields
    };
    const res = await fetch('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      showToast(data.error || 'Failed to save settings.', 'error');
      return null;
    }
    appState.settings = data;
    if (settingsRelayUrl) settingsRelayUrl.value = data.relayUrl || 'ws://localhost:8386';
    if (settingsHostRelay) settingsHostRelay.checked = !!data.hostRelay;
    if (settingsRelayPort) settingsRelayPort.value = data.relayPort || 8386;
    syncWanControls();
    showToast(successMessage, 'success');
    renderAll();
    return data;
  } catch (err) {
    showToast(err.message, 'error');
    return null;
  }
}

async function updateSyncCode(syncCode) {
  try {
    const relayUrl = wanRelayUrlInput?.value.trim() || appState.settings.relayUrl;
    if (relayUrl && relayUrl !== appState.settings.relayUrl) {
      await saveSettings({ relayUrl }, 'Relay URL saved.');
    }
    const res = await fetch('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ deviceName: appState.settings.deviceName, relayUrl, syncCode })
    });
    if (res.ok) {
      showToast(syncCode ? `Joined WAN Room: "${syncCode}"` : 'Left WAN room.', syncCode ? 'success' : 'info');
    } else {
      const err = await res.json(); showToast(err.error, 'error');
    }
  } catch (err) { showToast(`Failed: ${err.message}`, 'error'); }
}

async function runDirectoryScan() {
  btnRunScan.disabled = true;
  btnRunScan.textContent = '🔎 Scanning...';
  if (btnRunScanInner) { btnRunScanInner.disabled = true; btnRunScanInner.textContent = 'Scanning...'; }
  showToast('Scanning for emulator & repack saves...', 'info');
  try {
    const res = await fetch('/api/presets/scan');
    if (!res.ok) throw new Error(`Scan returned code ${res.status}`);
    const discovered = await res.json();
    discoveredSavesList = discovered;
    renderScanResults(discovered);
    showToast(`Scan complete! ${discovered.length} save folders found.`, 'success');
  } catch (err) {
    showToast(`Scan failed: ${err.message}`, 'error');
  } finally {
    btnRunScan.disabled = false;
    btnRunScan.textContent = '🔎 Scan System';
    if (btnRunScanInner) { btnRunScanInner.disabled = false; btnRunScanInner.textContent = 'Scan Saves'; }
  }
}

function toggleRelayContainers(isHosting) {
  relayPortConfigContainer?.classList.toggle('hidden', !isHosting);
  relayIpsConfigContainer?.classList.toggle('hidden', !isHosting);
}

function toggleAutoDeleteDays(enabled) {
  autoDeleteDaysContainer?.classList.toggle('hidden', !enabled);
}

function toggleCloudSyncFields() {
  if (!settingsCloudEnabled || !cloudSyncFields) return;
  const enabled = settingsCloudEnabled.checked;
  cloudSyncFields.classList.toggle('hidden', !enabled);
  if (enabled) {
    selectCloudProvider(selectedCloudProvider);
  }
}

/**
 * Triggers the OAuth sign-in popup for the given cloud provider.
 * Shows a loading state on the card itself during the auth flow.
 */
async function triggerOAuthConnect(provider) {
  const card = document.querySelector(`.provider-card[data-provider="${provider}"]`);
  const statusEl = card?.querySelector('.provider-status');
  const iconEl = card?.querySelector('.provider-icon');

  // Show loading state on card
  if (card) card.style.pointerEvents = 'none';
  const origStatusText = statusEl?.textContent || '';
  const origIconHtml = iconEl?.innerHTML || '';
  if (statusEl) statusEl.textContent = 'Connecting...';
  if (iconEl) iconEl.innerHTML = `<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="animation:spin 1s linear infinite;opacity:0.6"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>`;

  try {
    const res = await fetch('/api/auth/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Authentication failed.');

    const providerLabel = provider === 'google_drive' ? 'Google Drive' : provider === 'onedrive' ? 'OneDrive' : 'Dropbox';
    showToast(`✅ Connected to ${providerLabel}!`, 'success');

    // Restore original card icon before updating statuses
    if (iconEl) iconEl.innerHTML = origIconHtml;

    // Update local state immediately so card turns green without a page reload
    appState.settings.cloudSync = {
      ...appState.settings.cloudSync,
      enabled: true,
      provider,
      tokens: { userEmail: data.email }
    };
    updateProviderCardsStatuses();
    selectCloudProvider(provider);
  } catch (err) {
    showToast(err.message, 'error');
    // Restore original card appearance on failure
    if (statusEl) statusEl.textContent = origStatusText;
    if (iconEl) iconEl.innerHTML = origIconHtml;
  } finally {
    if (card) card.style.pointerEvents = '';
  }
}


function selectCloudProvider(provider) {
  selectedCloudProvider = provider;

  const isOAuth = provider === 'google_drive' || provider === 'onedrive' || provider === 'dropbox';

  // Only highlight non-OAuth cards with the cyan .active ring.
  // OAuth cards use .connected (green) when signed in, or just look clickable when not.
  document.querySelectorAll('.provider-card').forEach(card => {
    const p = card.getAttribute('data-provider');
    card.classList.toggle('active', !isOAuth && p === provider);
  });

  // Toggle visibility of configuration sub-forms
  cloudFormLocal?.classList.toggle('hidden', provider !== 'local');
  cloudFormWebdav?.classList.toggle('hidden', provider !== 'webdav');
  cloudFormWebhook?.classList.toggle('hidden', provider !== 'webhook');

  // Show the custom OAuth config section for OAuth providers
  const cloudOauthConfig = document.getElementById('cloud-oauth-config');
  cloudOauthConfig?.classList.toggle('hidden', !isOAuth);

  if (isOAuth) {
    let providerLabel = provider;
    if (provider === 'google_drive') providerLabel = 'Google Drive';
    else if (provider === 'onedrive') providerLabel = 'OneDrive';
    else if (provider === 'dropbox') providerLabel = 'Dropbox';

    // Update label in custom credentials section
    const lblProviderName = document.getElementById('lbl-custom-provider-name');
    if (lblProviderName) lblProviderName.textContent = providerLabel;

    // Load any saved custom client ID for this provider
    const cs = appState.settings.cloudSync || {};
    const customIds = cs.customClientIds || {};
    const customClientIdInput = document.getElementById('settings-cloud-client-id');
    if (customClientIdInput) {
      customClientIdInput.value = customIds[provider] || '';
      customClientIdInput.placeholder = provider === 'onedrive'
        ? 'Required: Enter your Azure App Client ID'
        : 'Leave blank to use OpenSave built-in credentials';
    }

    // Connected status bar: only show if this provider is the active, connected one
    const hasActiveCredentials = cs.provider === provider && cs.tokens?.userEmail && cs.enabled;
    cloudOauthDetails?.classList.toggle('hidden', !hasActiveCredentials);

    if (hasActiveCredentials) {
      if (cloudOauthTitle) cloudOauthTitle.textContent = `${providerLabel} Connected`;
      if (cloudOauthEmail) cloudOauthEmail.textContent = cs.tokens.userEmail;
    }
  } else {
    // Hide the connected status bar for non-OAuth providers
    cloudOauthDetails?.classList.add('hidden');
  }
}

function updateProviderCardsStatuses() {
  const cs = appState.settings.cloudSync || {};
  
  // Clean classes from all cards
  const cards = document.querySelectorAll('.provider-card');
  cards.forEach(card => {
    card.classList.remove('connected');
    const badge = card.querySelector('.provider-badge');
    if (badge) badge.remove();
  });

  // Update status labels
  // Google Drive
  const googleStatus = document.getElementById('status-google');
  if (googleStatus) {
    if (cs.provider === 'google_drive' && cs.tokens?.userEmail && cs.enabled) {
      googleStatus.textContent = cs.tokens.userEmail;
      const card = document.getElementById('card-provider-google');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      googleStatus.textContent = 'Click to sign in';
    }
  }

  // OneDrive
  const onedriveStatus = document.getElementById('status-onedrive');
  if (onedriveStatus) {
    if (cs.provider === 'onedrive' && cs.tokens?.userEmail && cs.enabled) {
      onedriveStatus.textContent = cs.tokens.userEmail;
      const card = document.getElementById('card-provider-onedrive');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      onedriveStatus.textContent = 'Click to sign in';
    }
  }

  // Dropbox
  const dropboxStatus = document.getElementById('status-dropbox');
  if (dropboxStatus) {
    if (cs.provider === 'dropbox' && cs.tokens?.userEmail && cs.enabled) {
      dropboxStatus.textContent = cs.tokens.userEmail;
      const card = document.getElementById('card-provider-dropbox');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      dropboxStatus.textContent = 'Click to sign in';
    }
  }


  // Local Folder
  const localStatus = document.getElementById('status-local');
  if (localStatus) {
    if (cs.provider === 'local' && cs.url && cs.enabled) {
      localStatus.textContent = `Configured`;
      const card = document.getElementById('card-provider-local');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      localStatus.textContent = cs.url ? 'Not enabled' : 'Not configured';
    }
  }

  // WebDAV
  const webdavStatus = document.getElementById('status-webdav');
  if (webdavStatus) {
    if (cs.provider === 'webdav' && cs.url && cs.enabled) {
      webdavStatus.textContent = `Configured`;
      const card = document.getElementById('card-provider-webdav');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      webdavStatus.textContent = cs.url ? 'Not enabled' : 'Not configured';
    }
  }

  // Webhook
  const webhookStatus = document.getElementById('status-webhook');
  if (webhookStatus) {
    if (cs.provider === 'webhook' && cs.url && cs.enabled) {
      webhookStatus.textContent = `Configured`;
      const card = document.getElementById('card-provider-webhook');
      card?.classList.add('connected');
      addCardBadge(card, 'ON');
    } else {
      webhookStatus.textContent = cs.url ? 'Not enabled' : 'Not configured';
    }
  }
}

function addCardBadge(card, text) {
  if (!card) return;
  const badge = document.createElement('div');
  badge.className = 'provider-badge';
  badge.textContent = text;
  card.appendChild(badge);
}

function encodePin(ip, port) {
  if (!ip) return null;
  const parts = ip.split('.').map(Number);
  if (parts.length !== 4) return null;
  const [a, b, c, d] = parts;

  let classType = 3;
  let ipData = 0;

  if (a === 192 && b === 168) {
    classType = 0;
    ipData = (c << 8) | d;
  } else if (a === 10) {
    classType = 1;
    ipData = (b << 16) | (c << 8) | d;
  } else if (a === 172 && b >= 16 && b <= 31) {
    classType = 2;
    ipData = ((b - 16) << 16) | (c << 8) | d;
  } else {
    return null; // unsupported for PIN
  }

  let portOffset = port - 8380;
  if (portOffset < 0 || portOffset > 31) {
    portOffset = 3; // default offset for 8383
  }

  const val = (classType * 536870912) + (ipData * 32) + portOffset;
  const pinCode = val.toString(36).toUpperCase().padStart(6, '0');
  return `SS-LAN-${pinCode}`;
}

function decodePin(pin) {
  if (!pin) return null;
  const cleaned = pin.trim().toUpperCase();
  if (!cleaned.startsWith('SS-LAN-')) return null;
  const pinCode = cleaned.replace(/^SS-LAN-/, '');
  if (!/^[A-Z0-9]{6}$/.test(pinCode)) return null;

  const val = parseInt(pinCode.toLowerCase(), 36);
  if (isNaN(val)) return null;

  const classType = Math.floor(val / 536870912) & 0x03;
  const ipData = Math.floor((val % 536870912) / 32) & 0xFFFFFF;
  const portOffset = val % 32;

  let ip = null;
  if (classType === 0) {
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `192.168.${c}.${d}`;
  } else if (classType === 1) {
    const b = (ipData >> 16) & 0xFF;
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `10.${b}.${c}.${d}`;
  } else if (classType === 2) {
    const b = ((ipData >> 16) & 0xFF) + 16;
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `172.${b}.${c}.${d}`;
  } else {
    return null;
  }

  const port = 8380 + portOffset;
  return { ip, port };
}

async function loadRelayIps() {
  if (settingsLocalIps) settingsLocalIps.textContent = 'Loading...';
  if (settingsPublicIp) settingsPublicIp.textContent = 'Loading...';
  if (settingsLocalSyncPin) settingsLocalSyncPin.textContent = 'Loading...';
  if (localSyncPinVal) localSyncPinVal.textContent = 'Loading...';
  if (wanLocalIps) wanLocalIps.textContent = 'Loading...';
  if (wanPublicIp) wanPublicIp.textContent = 'Loading...';
  try {
    const res = await fetch('/api/relay/ips');
    if (res.ok) {
      const data = await res.json();
      const port = appState.settings.relayPort || parseInt(wanRelayPortInput?.value, 10) || 8386;
      const lanUrls = (data.localIps || []).map(ip => `ws://${ip}:${port}`).join(', ') || 'None';
      const publicUrl = data.publicIp && !data.publicIp.startsWith('Could not') ? `ws://${data.publicIp}:${port}` : 'Unavailable';
      if (settingsLocalIps) settingsLocalIps.textContent = data.localIps?.join(', ') || 'None';
      if (settingsPublicIp) settingsPublicIp.textContent = data.publicIp || 'Unavailable';
      if (wanLocalIps) wanLocalIps.textContent = lanUrls;
      if (wanPublicIp) wanPublicIp.textContent = publicUrl;

      // Generate local PIN from first local IP and daemon port
      const firstIp = data.localIps && data.localIps.length > 0 ? data.localIps[0] : null;
      const dPort = appState.settings.port || 8383;
      if (firstIp) {
        const pin = encodePin(firstIp, dPort);
        if (pin) {
          if (settingsLocalSyncPin) settingsLocalSyncPin.textContent = pin;
          if (localSyncPinVal) localSyncPinVal.textContent = pin;
        } else {
          if (settingsLocalSyncPin) settingsLocalSyncPin.textContent = 'N/A (Non-LAN IP)';
          if (localSyncPinVal) localSyncPinVal.textContent = 'N/A';
        }
      } else {
        if (settingsLocalSyncPin) settingsLocalSyncPin.textContent = 'N/A (No LAN IP)';
        if (localSyncPinVal) localSyncPinVal.textContent = 'N/A';
      }
    }
  } catch (_) {
    if (settingsLocalIps) settingsLocalIps.textContent = 'Failed';
    if (settingsPublicIp) settingsPublicIp.textContent = 'Failed';
    if (settingsLocalSyncPin) settingsLocalSyncPin.textContent = 'Failed';
    if (localSyncPinVal) localSyncPinVal.textContent = 'Failed';
    if (wanLocalIps) wanLocalIps.textContent = 'Failed';
    if (wanPublicIp) wanPublicIp.textContent = 'Failed';
  }
}

// ============================================================
// CLOUD RELAY HEALTH
// ============================================================
async function loadRelayHealth() {
  const dot   = document.getElementById('relay-health-dot');
  const label = document.getElementById('relay-health-label');
  const meta  = document.getElementById('relay-health-meta');
  const btn   = document.getElementById('btn-use-cloud-relay');
  if (!dot || !label) return;

  // Indicate checking
  dot.className     = 'relay-health-dot checking';
  label.textContent = 'Checking relay…';
  if (meta) meta.textContent = '';

  // Show/hide the cloud relay button depending on current URL
  const currentUrl = appState.settings.relayUrl || '';
  const isCloud    = currentUrl === CLOUD_RELAY_URL;
  if (btn) btn.style.display = isCloud ? 'none' : '';

  try {
    const t0  = Date.now();
    const res = await fetch('/api/relay/health', { signal: AbortSignal.timeout(7000) });
    const ms  = Date.now() - t0;
    const data = await res.json().catch(() => ({}));

    if (res.ok && data.reachable) {
      dot.className     = 'relay-health-dot online';
      label.textContent = isCloud ? '☁️ Cloud relay online' : 'Relay online';
      if (meta) meta.textContent = `${ms}ms · ${data.clients ?? 0} clients · ${data.rooms ?? 0} rooms`;
    } else {
      dot.className     = 'relay-health-dot offline';
      label.textContent = isCloud ? '☁️ Cloud relay unreachable' : 'Relay unreachable';
      if (meta) meta.textContent = data.error ? data.error.slice(0, 60) : '';
    }
  } catch (err) {
    dot.className     = 'relay-health-dot offline';
    label.textContent = 'Relay check failed';
    if (meta) meta.textContent = err.message.slice(0, 60);
  }
}

// ============================================================
// RENDERING
// ============================================================
function renderAll() {
  // Apply UI mode layout styling and shunts
  applyUiMode();

  // Device name
  if (localDeviceName) localDeviceName.textContent = appState.settings.deviceName || 'Local PC';
  syncWanControls();

  // WAN room
  const syncCode = appState.settings.syncCode;
  if (syncCode) {
    formWanSync?.classList.add('hidden');
    activeWanRoomDisplay?.classList.remove('hidden');
    if (wanRoomNameLbl) wanRoomNameLbl.textContent = syncCode;
    if (wanCodeInput) wanCodeInput.value = syncCode;
  } else {
    formWanSync?.classList.remove('hidden');
    activeWanRoomDisplay?.classList.add('hidden');
    if (wanRoomNameLbl) wanRoomNameLbl.textContent = 'None';
    if (wanCodeInput) wanCodeInput.value = '';
  }

  renderWanRoom();
  renderGames();
  renderPeers();
  updateStats();
  if (activeGameId) renderDrawerDetails();
  updateConsoleDevices();
}

function applyUiMode() {
  const body = document.body;
  
  // 1. Apply body class
  body.classList.remove('layout-classic');
  body.classList.add('layout-modern');
  
  // 2. Move drawer controls DOM node
  const drawerHeader = document.querySelector('.drawer-header');
  const drawerControls = document.querySelector('.drawer-controls');
  const timelineTree = document.getElementById('timeline-tree');
  
  if (drawerHeader && drawerControls && timelineTree) {
    // Make sure drawer-control-dashboard container exists
    let dashboard = document.getElementById('drawer-control-dashboard');
    if (!dashboard) {
      dashboard = document.createElement('div');
      dashboard.id = 'drawer-control-dashboard';
      dashboard.className = 'drawer-control-dashboard';
      // Add a title header
      const header = document.createElement('h4');
      header.textContent = 'Control Panel';
      dashboard.appendChild(header);
      
      // Insert it right before timelineTree inside its parent
      timelineTree.parentNode.insertBefore(dashboard, timelineTree);
    }
    // Move controls inside dashboard if it isn't already
    if (drawerControls.parentNode !== dashboard) {
      dashboard.appendChild(drawerControls);
    }
  }
}


function renderGames() {
  const gamesList = Object.values(appState.games);
  const navBadge = document.getElementById('nav-badge-games');
  if (navBadge) navBadge.textContent = gamesList.length;

  if (gamesList.length === 0) {
    gamesGrid.innerHTML = `
      <div class="empty-state">
        <div class="empty-icon">🎮</div>
        <h3>No save folders tracked yet</h3>
        <p>Track a custom directory or run the scanner below to discover emulators &amp; repack saves automatically.</p>
        <button id="btn-empty-add-game-inner" class="btn-primary">Track a Game</button>
      </div>
    `;
    document.getElementById('btn-empty-add-game-inner')?.addEventListener('click', () => openModal(addGameModal));
    return;
  }

  gamesGrid.innerHTML = '';
  gamesList.forEach(game => {
    const card = document.createElement('article');
    card.className = 'game-card';

    const branch   = game.branches[game.activeBranch];
    const snapCount = branch ? branch.snapshots.length : 0;

    // Determine cover URL
    let coverSrc = '';
    if (game.coverUrl) {
      coverSrc = game.coverUrl;
    } else if (game.appId) {
      coverSrc = `https://shared.fastly.steamstatic.com/store_item_assets/steam/apps/${game.appId}/library_600x900.jpg`;
    }

    const coverHtml = coverSrc 
      ? `<div class="game-card-cover" style="background-image: url('${coverSrc}');"></div>`
      : `<div class="game-card-cover">🎮</div>`;

    let badgeClass = 'sync-badge local-only';
    let badgeText = 'Local Only';
    let indicatorColor = 'var(--text-3)';

    if (game.syncStatus === 'synced') {
      badgeClass = 'sync-badge synced';
      badgeText = 'Synced';
      indicatorColor = 'var(--emerald)';
    } else if (game.syncStatus === 'out-of-sync') {
      badgeClass = 'sync-badge out-of-sync';
      badgeText = 'Out of Sync';
      indicatorColor = 'var(--orange)';
    }

    const canLaunch = !!(game.appId || game.exePath);

    card.innerHTML = `
      ${coverHtml}
      <div class="game-card-body">
        <div class="game-card-header">
          <h3 class="game-title" style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 170px;" title="${game.name}">${game.name}</h3>
          <span class="game-branch-badge">${game.activeBranch}</span>
        </div>
        <p class="game-path-text" style="display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; height: 28px;" title="${game.savePath}">${game.savePath}</p>
        <div class="game-footer-info" style="margin-top: 0; padding-top: 6px;">
          <span class="backup-count-info">Backups: <strong>${snapCount}</strong></span>
          <span class="${badgeClass}">
            <span class="pulse-indicator" style="background:${indicatorColor}; box-shadow:0 0 6px ${indicatorColor};"></span> ${badgeText}
          </span>
        </div>
      </div>
      <!-- Hover quick-action buttons -->
      <div class="game-card-hover-actions">
        <button class="card-quick-action btn-quick-launch ${canLaunch ? '' : 'disabled'}" data-game-id="${game.id}" title="${canLaunch ? 'Launch Game' : 'No launch executable configured'}" ${canLaunch ? '' : 'disabled'}>🎮</button>
        <button class="card-quick-action btn-quick-sync" data-game-id="${game.id}" title="Sync P2P Now">⚡</button>
        <button class="card-quick-action btn-quick-snapshot" data-game-id="${game.id}" title="Save Snapshot">📸</button>
        <button class="card-quick-action btn-quick-details" data-game-id="${game.id}" title="View Timeline & Settings">ℹ️</button>
      </div>
    `;

    card.addEventListener('click', (e) => {
      const btn = e.target.closest('.card-quick-action');
      if (btn) {
        e.stopPropagation();
        e.preventDefault();
        const gameId = btn.getAttribute('data-game-id');
        if (btn.classList.contains('btn-quick-launch')) {
          triggerGameLaunch(gameId);
        } else if (btn.classList.contains('btn-quick-sync')) {
          triggerGameSync(gameId);
        } else if (btn.classList.contains('btn-quick-snapshot')) {
          triggerGameSnapshot(gameId);
        } else if (btn.classList.contains('btn-quick-details')) {
          openDrawer(gameId);
        }
        return;
      }
      openDrawer(game.id);
    });
    gamesGrid.appendChild(card);
  });
}

// QUICK-ACTION HELPERS FOR GAME CARDS
async function triggerGameLaunch(gameId) {
  try {
    const res = await fetch(`/api/games/${gameId}/launch`, { method: 'POST' });
    if (res.ok) {
      showToast('Game launch requested.', 'success');
    } else {
      const err = await res.json();
      showToast(err.error || 'Launch failed', 'error');
    }
  } catch (err) {
    showToast('Launch error: ' + err.message, 'error');
  }
}

async function triggerGameSync(gameId) {
  try {
    showToast('Sync started...', 'info');
    const res = await fetch(`/api/games/${gameId}/sync`, { method: 'POST' });
    if (res.ok) {
      showToast('Sync triggered successfully.', 'success');
    } else {
      const err = await res.json();
      showToast(`Sync failed: ${err.error}`, 'error');
    }
  } catch (err) {
    showToast('Sync error: ' + err.message, 'error');
  }
}

function triggerGameSnapshot(gameId) {
  activeGameId = gameId;
  openModal(snapshotCommentModal);
}

function renderScanResults(discovered) {
  if (discovered.length === 0) {
    btnTrackAll.classList.add('hidden');
    btnUntrackAll.classList.add('hidden');
    scanResultsGrid.innerHTML = `
      <div class="empty-state">
        <div class="empty-icon">🔎</div>
        <h3>No emulator or repack saves found</h3>
        <p>Ensure emulators (Dolphin, RPCS3, Ryujinx, etc.) have active saves, or track a custom folder.</p>
        <button id="btn-run-scan-inner-2" class="btn-primary">Scan Again</button>
      </div>
    `;
    document.getElementById('btn-run-scan-inner-2')?.addEventListener('click', runDirectoryScan);
    return;
  }

  const untrackedCount = discovered.filter(item =>
    !Object.values(appState.games).some(g => g.savePath.toLowerCase() === item.savePath.toLowerCase())
  ).length;
  const trackedCount = discovered.filter(item =>
    Object.values(appState.games).some(g => g.savePath.toLowerCase() === item.savePath.toLowerCase())
  ).length;

  if (untrackedCount > 0) { btnTrackAll.classList.remove('hidden'); btnTrackAll.textContent = `➕ Track All (${untrackedCount})`; }
  else { btnTrackAll.classList.add('hidden'); }
  if (trackedCount > 0) { btnUntrackAll.classList.remove('hidden'); btnUntrackAll.textContent = `❌ Untrack All (${trackedCount})`; }
  else { btnUntrackAll.classList.add('hidden'); }

  scanResultsGrid.innerHTML = '';
  discovered.forEach(item => {
    const isTracked = Object.values(appState.games).some(g => g.savePath.toLowerCase() === item.savePath.toLowerCase());
    const card = document.createElement('article');
    card.className = 'game-card';
    if (item.type === 'emulator') card.style.borderColor = 'rgba(6,182,212,0.3)';
    else if (item.type === 'repack') card.style.borderColor = 'rgba(249,115,22,0.3)';

    // Determine cover URL for the scanner card
    let coverSrc = '';
    if (item.appId) {
      coverSrc = `https://shared.fastly.steamstatic.com/store_item_assets/steam/apps/${item.appId}/library_600x900.jpg`;
    }

    const coverHtml = coverSrc 
      ? `<div class="game-card-cover" style="background-image: url('${coverSrc}');"></div>`
      : `<div class="game-card-cover">🎮</div>`;

    // Safely encode appId for embedding in onclick attribute
    const appIdAttr = item.appId ? String(item.appId) : '';

    card.innerHTML = `
      ${coverHtml}
      <div class="game-card-body">
        <div class="game-card-header">
          <h3 class="game-title" style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 170px;" title="${item.name}">${item.name}</h3>
          <span class="game-branch-badge" style="background:rgba(255,255,255,0.05);color:var(--text-3);border:none;">${item.type.toUpperCase()}</span>
        </div>
        <p class="game-path-text" style="display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; height: 28px;" title="${item.savePath}">${item.savePath}</p>
        <div class="game-footer-info" style="margin-top: 0; padding-top: 6px; align-items:center;">
          <span class="backup-count-info" style="font-size:11px;">Status: <strong>${isTracked ? 'Monitored' : 'Not Monitored'}</strong></span>
          ${isTracked
            ? `<span style="color:var(--emerald);font-weight:600;font-size:11px;">✓ Active</span>`
            : `<button class="btn-peer-action" onclick="trackDiscoveredSave('${item.name.replace(/'/g, "\\'")}', '${item.savePath.replace(/\\/g, '\\\\')}', '${appIdAttr}')">+ Track</button>`
          }
        </div>
      </div>
    `;
    scanResultsGrid.appendChild(card);
  });
}

function renderPeers() {
  const paired   = Object.values(appState.peers || {});
  const discovered = appState.discoveredPeers || [];
  const requests = appState.pairingRequests || [];

  const navBadge = document.getElementById('nav-badge-peers');
  if (navBadge) navBadge.textContent = paired.filter(p => p.status === 'online').length;

  // Paired
  if (paired.length === 0) {
    pairedPeersList.innerHTML = `<li class="empty-list-msg">No devices paired. Add a peer via IP/port or approve a LAN discovery request.</li>`;
  } else {
    pairedPeersList.innerHTML = '';
    paired.forEach(peer => {
      const li = document.createElement('li');
      const isWan = peer.address === 'relay';
      li.innerHTML = `
        <div class="peer-info">
          <span class="peer-name-txt">
            <span class="peer-status-dot ${peer.status}"></span>
            ${peer.name} ${isWan ? '<span class="badge-pill" style="font-size:8px;padding:1px 5px;">WAN</span>' : ''}
          </span>
          <span class="peer-ip-txt">${isWan ? 'Connected via Relay' : `${peer.address}:${peer.port}`}</span>
        </div>
        <button class="btn-secondary btn-sm" onclick="removePeer('${peer.id}')">Unpair</button>
      `;
      pairedPeersList.appendChild(li);
    });
  }

  // Discovered
  const unpairedDiscovered = discovered.filter(p => !paired.some(pp => pp.id === p.id));
  if (unpairedDiscovered.length === 0) {
    discoveredPeersList.innerHTML = `<li class="empty-list-msg">Scanning network...</li>`;
  } else {
    discoveredPeersList.innerHTML = '';
    unpairedDiscovered.forEach(peer => {
      const isWan = peer.address === 'relay' || peer.isWan;
      const li = document.createElement('li');
      li.innerHTML = `
        <div class="peer-info">
          <span class="peer-name-txt">${isWan ? '🌐' : '📡'} ${peer.deviceName}</span>
          <span class="peer-ip-txt">${isWan ? 'Discovered via WAN Room' : `${peer.address}:${peer.port}`}</span>
        </div>
        <button class="btn-primary btn-sm" onclick="sendOutboundPairing('${peer.address}', ${peer.port}, ${isWan}, '${peer.id || ''}')">Pair</button>
      `;
      discoveredPeersList.appendChild(li);
    });
  }

  // Pairing requests
  if (requests.length === 0) {
    pairingRequestsPanel?.classList.add('hidden');
  } else {
    pairingRequestsPanel?.classList.remove('hidden');
    pairingRequestsList.innerHTML = '';
    requests.forEach(req => {
      const li = document.createElement('li');
      li.innerHTML = `
        <div class="peer-info">
          <span class="peer-name-txt">⚠️ ${req.deviceName}</span>
          <span class="peer-ip-txt">${req.address === 'relay' ? 'Pairing via WAN' : `${req.address}:${req.port}`} wants to pair</span>
        </div>
        <div style="display:flex;gap:6px;">
          <button class="btn-accent btn-sm" onclick="approvePairing('${req.peerId}')">✓ Approve</button>
          <button class="btn-danger btn-sm" onclick="rejectPairing('${req.peerId}')">&times;</button>
        </div>
      `;
      pairingRequestsList.appendChild(li);
    });
  }
}

function updateGlobalRelayStatus() {
  if (!titlebarRelayStatus || !relayStatusText) return;

  const wanRoom = appState.wanRoom || {
    enabled: !!appState.settings.syncCode,
    connected: false,
    state: appState.settings.syncCode ? 'connecting' : 'disconnected'
  };

  if (!wanRoom.enabled) {
    titlebarRelayStatus.style.display = 'none';
    return;
  }

  titlebarRelayStatus.style.display = 'flex';
  
  // Remove existing state classes
  titlebarRelayStatus.classList.remove('online', 'offline', 'checking');

  if (wanRoom.connected) {
    titlebarRelayStatus.classList.add('online');
    relayStatusText.textContent = 'Cloud Online';
  } else if (wanRoom.state === 'connecting') {
    titlebarRelayStatus.classList.add('checking');
    relayStatusText.textContent = 'Cloud Connecting...';
  } else if (wanRoom.state === 'error') {
    titlebarRelayStatus.classList.add('offline');
    relayStatusText.textContent = 'Cloud Error';
  } else {
    titlebarRelayStatus.classList.add('offline');
    relayStatusText.textContent = 'Cloud Offline';
  }
}

function renderWanRoom() {
  updateGlobalRelayStatus();
  const wanRoom = appState.wanRoom || {
    enabled: !!appState.settings.syncCode,
    connected: false,
    state: appState.settings.syncCode ? 'connecting' : 'disconnected',
    relayUrl: appState.settings.relayUrl,
    roomCode: appState.settings.syncCode,
    peers: []
  };

  const peers = wanRoom.peers || (appState.discoveredPeers || []).filter(p => p.address === 'relay' || p.isWan);
  const paired = appState.peers || {};
  const onlinePeers = peers.filter(peer => peer.online !== false);

  if (wanRelayUrlLbl) {
    wanRelayUrlLbl.textContent = wanRoom.relayUrl || appState.settings.relayUrl || 'Not configured';
  }
  if (wanRoomPeerCount) {
    wanRoomPeerCount.textContent = `${onlinePeers.length} ONLINE`;
  }
  if (wanConnectionState) {
    const state = wanRoom.connected ? 'CONNECTED' : (wanRoom.state || 'DISCONNECTED').toUpperCase();
    wanConnectionState.textContent = state;
    wanConnectionState.classList.toggle('badge-warning', !wanRoom.connected);
    wanConnectionState.style.background = wanRoom.connected ? 'rgba(16,185,129,0.12)' : '';
    wanConnectionState.style.color = wanRoom.connected ? 'var(--emerald)' : '';
    wanConnectionState.style.borderColor = wanRoom.connected ? 'rgba(16,185,129,0.28)' : '';
  }

  if (!wanRoomPeersList) return;

  if (!wanRoom.enabled) {
    wanRoomPeersList.innerHTML = `<li class="empty-list-msg">Join a room to see relay peers.</li>`;
    return;
  }

  if (wanRoom.error && !wanRoom.connected) {
    wanRoomPeersList.innerHTML = `<li class="empty-list-msg">Relay error: ${wanRoom.error}</li>`;
    return;
  }

  if (peers.length === 0) {
    wanRoomPeersList.innerHTML = `<li class="empty-list-msg">Waiting for other devices in this room...</li>`;
    return;
  }

  wanRoomPeersList.innerHTML = '';
  peers.forEach(peer => {
    const isPaired = !!paired[peer.id] || peer.paired;
    const li = document.createElement('li');
    li.innerHTML = `
      <div class="peer-info">
        <span class="peer-name-txt">
          <span class="peer-status-dot ${peer.online === false ? 'offline' : 'online'}"></span>
          ${peer.deviceName || 'Unknown Device'}
        </span>
        <span class="peer-ip-txt">${peer.deviceType || 'desktop'} - ${isPaired ? 'Paired' : 'Ready to pair'}</span>
      </div>
      ${isPaired
        ? `<span style="color:var(--emerald);font-weight:600;font-size:11px;">Paired</span>`
        : `<button class="btn-primary btn-sm" onclick="sendOutboundPairing('relay', ${peer.port || 0}, true, '${peer.id}')">Pair</button>`
      }
    `;
    wanRoomPeersList.appendChild(li);
  });
}

function updateStats() {
  const gamesList  = Object.values(appState.games);
  const pairedPeers = Object.values(appState.peers || {});
  if (statGamesCount) statGamesCount.textContent = gamesList.length;
  if (statPeersCount) statPeersCount.textContent = pairedPeers.filter(p => p.status === 'online').length;

  let totalBackups = 0;
  gamesList.forEach(g => {
    for (const b in g.branches) totalBackups += g.branches[b].snapshots?.length || 0;
  });
  if (statBackupsCount) statBackupsCount.textContent = totalBackups;
}

// ============================================================
// DRAWER
// ============================================================
function openDrawer(gameId) {
  activeGameId = gameId;
  gameDetailsDrawer.classList.remove('hidden');
  renderDrawerDetails();
}

function closeDrawer() {
  activeGameId = null;
  gameDetailsDrawer.classList.add('hidden');
}

function renderDrawerDetails() {
  const game = appState.games[activeGameId];
  if (!game) { closeDrawer(); return; }

  // Toggle Cloud Explorer button state
  if (btnDrawerCloudExplorer) {
    const hasCloud = appState.settings.cloudSync?.enabled;
    btnDrawerCloudExplorer.classList.toggle('disabled', !hasCloud);
    btnDrawerCloudExplorer.title = hasCloud ? 'Browse cloud backup snapshots' : 'Enable Cloud Backup in Settings to browse cloud snapshots';
  }

  drawerGameName.textContent = game.name;
  drawerGamePath.textContent = game.savePath;

  // Populate launch configuration form fields
  if (drawerGameAppid) drawerGameAppid.value = game.appId || '';
  if (drawerGameExepath) drawerGameExepath.value = game.exePath || '';
  if (drawerGameCoverurl) drawerGameCoverurl.value = game.coverUrl || '';
  
  const drawerGameAutosync = document.getElementById('drawer-game-autosync');
  const drawerGameMaxsnapshots = document.getElementById('drawer-game-maxsnapshots');
  if (drawerGameAutosync) drawerGameAutosync.checked = game.autoSync !== false;
  if (drawerGameMaxsnapshots) drawerGameMaxsnapshots.value = (game.maxSnapshots !== undefined) ? game.maxSnapshots : 5;

  // Show or hide launch button based on launch config state
  if (btnDrawerLaunch) {
    if (game.appId || game.exePath) {
      btnDrawerLaunch.classList.remove('hidden');
    } else {
      btnDrawerLaunch.classList.add('hidden');
    }
  }

  // Handle drawer vertical cover art rendering
  if (drawerCoverImg && drawerCoverPlaceholder) {
    let coverSrc = '';
    if (game.coverUrl) {
      coverSrc = game.coverUrl;
    } else if (game.appId) {
      coverSrc = `https://shared.fastly.steamstatic.com/store_item_assets/steam/apps/${game.appId}/library_600x900.jpg`;
    }

    if (coverSrc) {
      drawerCoverImg.src = coverSrc;
      drawerCoverImg.classList.remove('hidden');
      drawerCoverPlaceholder.classList.add('hidden');
    } else {
      drawerCoverImg.src = '';
      drawerCoverImg.classList.add('hidden');
      drawerCoverPlaceholder.classList.remove('hidden');
    }
  }

  branchSelect.innerHTML = '';
  Object.keys(game.branches).forEach(bName => {
    const opt = document.createElement('option');
    opt.value = bName;
    opt.textContent = bName;
    if (bName === game.activeBranch) opt.selected = true;
    branchSelect.appendChild(opt);
  });

  const branch = game.branches[game.activeBranch];
  infoBranchName.textContent = game.activeBranch;
  const snapshots = branch ? branch.snapshots : [];
  infoSnapshotCount.textContent = snapshots.length;

  if (snapshots.length > 0) {
    infoLastBackup.textContent = new Date(snapshots[snapshots.length - 1].timestamp).toLocaleString();
  } else {
    infoLastBackup.textContent = 'Never';
  }

  if (snapshots.length === 0) {
    timelineTree.innerHTML = `<div class="empty-list-msg" style="padding-left:0;">No backups on this branch yet. Make changes or click "Save Snapshot".</div>`;
    return;
  }

  timelineTree.innerHTML = '';
  [...snapshots].reverse().forEach(snap => {
    const node = document.createElement('div');
    let nodeClass = '';
    if (snap.isSystemAuto) {
      nodeClass = snap.comment.includes('Pre-rollback') ? 'safety-point' : 'system-auto';
    }
    node.className = `timeline-node ${nodeClass}`;
    node.innerHTML = `
      <div class="node-dot"></div>
      <div class="node-card">
        <div class="node-meta">
          <span class="node-comment">${snap.comment}</span>
          <div class="node-details">
            <span>📅 ${new Date(snap.timestamp).toLocaleString()}</span>
            <span>💾 ${(snap.sizeBytes / 1024).toFixed(1)} KB</span>
            <span>ID: <code class="node-id-badge">${snap.id}</code></span>
          </div>
        </div>
        <div style="display:flex; gap:8px;">
          <button class="btn-rollback-action" onclick="triggerRollbackConfirmation('${snap.id}')">Rollback</button>
          <button class="btn-secondary btn-sm" onclick="openSnapshotBrowser('${game.id}', '${snap.id}')">Browse Files</button>
        </div>
      </div>
    `;
    timelineTree.appendChild(node);
  });
}

// ============================================================
// GLOBAL WINDOW ACTIONS
// ============================================================
window.trackDiscoveredSave = async (name, savePath, appId = null) => {
  const success = await trackFolder(name, savePath, appId || null);
  if (success) runDirectoryScan();
};

window.triggerRollbackConfirmation = (snapshotId) => {
  pendingRollbackSnapId = snapshotId;
  document.getElementById('rollback-target-id').textContent = snapshotId;
  openModal(rollbackConfirmModal);
};

window.sendOutboundPairing = async (address, port, isWan = false, targetPeerId = null) => {
  try {
    showToast(`Sending pairing request to ${isWan ? 'WAN Peer' : `${address}:${port}`}...`, 'info');
    const res = await fetch('/api/peers/pair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address, port, isWan, targetPeerId })
    });
    const data = await res.json();
    if (res.ok) showToast(`Pairing: ${data.message || 'Sent!'}`, 'success');
    else showToast(data.error, 'error');
  } catch (err) { showToast(err.message, 'error'); }
};

window.approvePairing = async (peerId) => {
  const res = await fetch('/api/peers/approve', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ peerId })
  });
  if (res.ok) showToast('Peer approved.', 'success');
};

window.rejectPairing = async (peerId) => {
  const res = await fetch('/api/peers/reject', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ peerId })
  });
  if (res.ok) showToast('Pairing rejected.', 'info');
};

window.removePeer = async (peerId) => {
  if (!confirm('Unpair from this device? Syncing will stop.')) return;
  const res = await fetch(`/api/peers/${peerId}`, { method: 'DELETE' });
  if (res.ok) showToast('Device unpaired.', 'info');
};

// ============================================================
// MODALS
// ============================================================
function openModal(modal) { modal.classList.remove('hidden'); }
function closeModal(modal) { modal.classList.add('hidden'); }

// ============================================================
// TOAST SYSTEM
// ============================================================
function showToast(message, type = 'info') {
  const container = document.getElementById('toast-container');
  if (!container) return;
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  const icons = { success: '✅', warning: '⚠️', error: '❌', info: 'ℹ️' };
  toast.innerHTML = `
    <span>${icons[type] || 'ℹ️'} ${message}</span>
    <button class="toast-close">&times;</button>
  `;
  toast.querySelector('.toast-close').addEventListener('click', () => toast.remove());
  container.appendChild(toast);
  setTimeout(() => {
    toast.style.animation = 'slideIn 0.3s reverse forwards';
    setTimeout(() => toast.remove(), 300);
  }, 4000);
}

// ============================================================
// CONFLICT RESOLUTION
// ============================================================
function checkActiveConflicts(activeConflicts) {
  if (!activeConflicts) return;
  for (const gameId in activeConflicts) {
    const conflict = activeConflicts[gameId];
    if (conflict && conflict.peer) {
      openConflictModal(gameId, conflict.peer.id, conflict.peer.name, conflict.localSnap, conflict.remoteSnap, conflict.diff);
      break;
    }
  }
}

function openConflictModal(gameId, peerId, peerName, localSnap, remoteSnap, diff) {
  activeConflictData = { gameId, peerId, peerName };
  const modal = document.getElementById('conflict-modal');
  if (!modal) return;
  document.getElementById('conflict-game-name').textContent = getGameName(gameId);
  document.getElementById('conflict-peer-name').textContent = peerName;
  document.getElementById('conflict-local-comment').textContent  = localSnap?.comment  || 'No local snapshots';
  document.getElementById('conflict-local-time').textContent     = localSnap ? new Date(localSnap.timestamp).toLocaleString() : 'Never';
  document.getElementById('conflict-local-id').textContent       = localSnap?.id || 'N/A';
  document.getElementById('conflict-remote-comment').textContent = remoteSnap?.comment || 'No remote snapshots';
  document.getElementById('conflict-remote-time').textContent    = remoteSnap ? new Date(remoteSnap.timestamp).toLocaleString() : 'Never';
  document.getElementById('conflict-remote-id').textContent      = remoteSnap?.id || 'N/A';

  const diffList = document.getElementById('conflict-diff-list');
  if (diffList) {
    diffList.innerHTML = '';
    let diffCount = 0;
    
    if (diff) {
      if (Array.isArray(diff.added)) {
        diff.added.forEach(file => {
          diffCount++;
          const li = document.createElement('li');
          li.className = 'conflict-diff-item added';
          li.innerHTML = `<span class="conflict-diff-file">${file}</span><span class="conflict-diff-type">Added on remote</span>`;
          diffList.appendChild(li);
        });
      }
      if (diff.modified) {
        Object.keys(diff.modified).forEach(file => {
          diffCount++;
          const li = document.createElement('li');
          li.className = 'conflict-diff-item modified';
          li.innerHTML = `<span class="conflict-diff-file">${file}</span><span class="conflict-diff-type">Modified</span>`;
          diffList.appendChild(li);
        });
      }
      if (Array.isArray(diff.deleted)) {
        diff.deleted.forEach(file => {
          diffCount++;
          const li = document.createElement('li');
          li.className = 'conflict-diff-item deleted';
          li.innerHTML = `<span class="conflict-diff-file">${file}</span><span class="conflict-diff-type">Deleted on remote</span>`;
          diffList.appendChild(li);
        });
      }
    }

    if (diffCount === 0) {
      diffList.innerHTML = `<li class="empty-list-msg" style="padding-left:0; color: var(--text-3);">No file-level differences found.</li>`;
    }
  }

  modal.classList.remove('hidden');
}

async function resolveActiveConflict(resolution) {
  if (!activeConflictData) return;
  const { gameId, peerId } = activeConflictData;
  const btns = ['btn-conflict-keep-local','btn-conflict-keep-remote','btn-conflict-keep-both']
    .map(id => document.getElementById(id));
  btns.forEach(b => b && (b.disabled = true));
  try {
    showToast('Resolving conflict...', 'info');
    const res = await fetch(`/api/games/${gameId}/resolve-conflict`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ peerId, resolution })
    });
    if (res.ok) {
      const data = await res.json();
      showToast(
        resolution === 'merge-branch'
          ? `Conflict resolved! Remote saves in branch: ${data.branchName}`
          : `Conflict resolved using ${resolution === 'keep-local' ? 'local' : 'remote'} saves!`,
        'success'
      );
      document.getElementById('conflict-modal').classList.add('hidden');
      activeConflictData = null;
    } else {
      const err = await res.json(); showToast(`Failed: ${err.error}`, 'error');
    }
  } catch (err) { showToast(`Error: ${err.message}`, 'error'); }
  finally { btns.forEach(b => b && (b.disabled = false)); }
}

// ============================================================
// CONSOLE / LOGS ENGINE
// ============================================================
function initConsole(history = []) {
  const consoleArea = document.getElementById('console-logs-area');
  if (!consoleArea) return;
  
  consoleArea.innerHTML = '';
  
  if (history && history.length > 0) {
    history.forEach(record => {
      appendLogLine(record, false);
    });
    consoleArea.scrollTop = consoleArea.scrollHeight;
  } else {
    appendLogLine({
      timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
      type: 'info',
      message: 'OpenSave Console Connection Initialized',
      meta: 'v1.0.0'
    }, false);
  }

  // Bind clear console button
  const btnClear = document.getElementById('btn-clear-console');
  if (btnClear) {
    btnClear.onclick = () => {
      consoleArea.innerHTML = '';
      appendLogLine({
        timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
        type: 'info',
        message: 'Console cleared',
        meta: ''
      });
    };
  }

  updateConsoleDevices();
}

function appendLogLine(record, scroll = true) {
  const consoleArea = document.getElementById('console-logs-area');
  if (!consoleArea) return;

  const line = document.createElement('div');
  line.className = `log-line ${record.type || 'info'}`;

  // Timestamp
  const ts = document.createElement('span');
  ts.className = 'log-timestamp';
  ts.textContent = record.timestamp;
  line.appendChild(ts);

  // Status indicator dot
  const dot = document.createElement('span');
  dot.className = `log-dot ${record.type || 'info'}`;
  line.appendChild(dot);

  // Message
  const msg = document.createElement('span');
  msg.className = 'log-message';
  msg.textContent = record.message;
  line.appendChild(msg);

  // Meta if exists
  if (record.meta) {
    const meta = document.createElement('span');
    meta.className = 'log-meta';
    meta.textContent = record.meta;
    line.appendChild(meta);
  }

  consoleArea.appendChild(line);

  // Keep logs list to max 300 elements
  while (consoleArea.childElementCount > 300) {
    consoleArea.removeChild(consoleArea.firstChild);
  }

  if (scroll) {
    consoleArea.scrollTop = consoleArea.scrollHeight;
  }
}

function updateConsoleDevices() {
  const localType = appState.settings.deviceType || 'desktop';
  
  // Clear active classes from all device cards
  document.querySelectorAll('.device-card').forEach(card => {
    card.classList.remove('active', 'is-local');
  });

  // Mark local device
  const localCard = document.getElementById(`dev-${localType}`);
  if (localCard) {
    localCard.classList.add('active', 'is-local');
  }

  // Check online paired peers and activate their corresponding categories
  const pairedPeersList = Object.values(appState.peers || {});
  pairedPeersList.forEach(peer => {
    if (peer.status === 'online') {
      const type = peer.deviceType || 'desktop';
      const card = document.getElementById(`dev-${type}`);
      if (card) {
        card.classList.add('active');
      }
    }
  });

  // Update console sync status label
  const consoleSyncLabel = document.getElementById('console-sync-status');
  if (consoleSyncLabel) {
    const statusText = document.getElementById('stat-sync-status')?.textContent || 'Idle';
    if (statusText === 'Syncing...') {
      consoleSyncLabel.textContent = '● Syncing delta saves...';
      consoleSyncLabel.style.color = 'var(--orange)';
    } else if (statusText === 'Error') {
      consoleSyncLabel.textContent = '● Connection error';
      consoleSyncLabel.style.color = 'var(--red)';
    } else {
      consoleSyncLabel.textContent = '● All devices synchronized';
      consoleSyncLabel.style.color = 'var(--emerald)';
    }
  }
}

// Custom Scan Paths Renderer & Handlers
function renderCustomScanPaths() {
  if (!customScanPathsList) return;
  customScanPathsList.innerHTML = '';
  if (localCustomScanPaths.length === 0) {
    customScanPathsList.innerHTML = `<div style="font-size:12px; color:var(--text-3); font-style:italic;">No custom scan paths configured.</div>`;
    return;
  }
  localCustomScanPaths.forEach((p, idx) => {
    const item = document.createElement('div');
    item.className = 'custom-path-item';
    item.style = 'display:flex; justify-content:space-between; align-items:center; background:rgba(255,255,255,0.03); border:1px solid var(--border); padding:6px 10px; border-radius:6px; font-size:12px; margin-bottom: 4px;';
    item.innerHTML = `
      <span style="font-family:var(--font-mono); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; max-width:400px;" title="${p}">${p}</span>
      <button type="button" class="btn-peer-action btn-danger" style="padding:2px 6px; background:rgba(239,68,68,0.15); color:var(--red);" onclick="removeCustomScanPath(${idx})">Remove</button>
    `;
    customScanPathsList.appendChild(item);
  });
}

window.removeCustomScanPath = (idx) => {
  localCustomScanPaths.splice(idx, 1);
  renderCustomScanPaths();
};

// Custom Path Translations Renderer & Handlers
function renderPathTranslations() {
  if (!pathTranslationsList) return;
  pathTranslationsList.innerHTML = '';
  if (localPathTranslations.length === 0) {
    pathTranslationsList.innerHTML = `<div style="font-size:12px; color:var(--text-3); font-style:italic;">No custom path translation rules.</div>`;
    return;
  }
  localPathTranslations.forEach((rule, idx) => {
    const item = document.createElement('div');
    item.className = 'custom-path-item';
    item.style = 'display:flex; flex-direction:column; gap:4px; background:rgba(255,255,255,0.03); border:1px solid var(--border); padding:8px 36px 8px 10px; border-radius:6px; font-size:11px; margin-bottom: 4px; position:relative;';
    item.innerHTML = `
      <div style="font-family:var(--font-mono); font-size:10px; color:var(--text-muted);">FROM: <span style="color:white; word-break:break-all;">${rule.fromPattern}</span></div>
      <div style="font-family:var(--font-mono); font-size:10px; color:var(--text-muted); margin-top:2px;">TO: <span style="color:white; word-break:break-all;">${rule.toPattern}</span></div>
      <button type="button" class="btn-peer-action btn-danger" style="position:absolute; right:8px; top:50%; transform:translateY(-50%); padding:2px 6px; background:rgba(239,68,68,0.15); color:var(--red);" onclick="removePathTranslation(${idx})">Delete</button>
    `;
    pathTranslationsList.appendChild(item);
  });
}

window.removePathTranslation = async (idx) => {
  localPathTranslations.splice(idx, 1);
  renderPathTranslations();
  await saveSettings({ pathTranslations: localPathTranslations }, '🔄 Path translation rule deleted!');
};

// ============================================================
// SNAPSHOT FILE BROWSER & GRANULAR RESTORE
// ============================================================
window.openSnapshotBrowser = async (gameId, snapshotId) => {
  const modal = document.getElementById('snapshot-browser-modal');
  const tableBody = document.getElementById('snapshot-files-table-body');
  if (!modal || !tableBody) return;

  document.getElementById('snapshot-browser-title').textContent = `📂 Browse Backup: ${snapshotId}`;
  tableBody.innerHTML = '<tr><td colspan="3" style="text-align:center; color:var(--text-3);">Loading files from snapshot...</td></tr>';
  openModal(modal);

  try {
    const res = await fetch(`/api/games/${gameId}/snapshot/${snapshotId}/files`);
    if (res.ok) {
      const data = await res.json();
      tableBody.innerHTML = '';
      if (data.files.length === 0) {
        tableBody.innerHTML = '<tr><td colspan="3" style="text-align:center; color:var(--text-3);">No files in this snapshot.</td></tr>';
        return;
      }
      data.files.forEach(file => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td style="font-family:var(--font-mono); font-size:11px; word-break:break-all;">${file.name}</td>
          <td>${(file.size / 1024).toFixed(1)} KB</td>
          <td style="text-align:right;">
            <button class="btn-restore-file-action" onclick="restoreSnapshotFile('${gameId}', '${snapshotId}', '${file.name.replace(/'/g, "\\'")}')">Restore File</button>
          </td>
        `;
        tableBody.appendChild(tr);
      });
    } else {
      const err = await res.json();
      tableBody.innerHTML = `<tr><td colspan="3" style="text-align:center; color:var(--red);">Failed to read snapshot: ${err.error}</td></tr>`;
    }
  } catch (err) {
    tableBody.innerHTML = `<tr><td colspan="3" style="text-align:center; color:var(--red);">Error: ${err.message}</td></tr>`;
  }
};

window.restoreSnapshotFile = async (gameId, snapshotId, relPath) => {
  if (!confirm(`Are you sure you want to restore only "${relPath}" from snapshot "${snapshotId}"?\nThis will overwrite the active file.`)) {
    return;
  }

  showToast(`Restoring ${relPath}...`, 'info');
  try {
    const res = await fetch(`/api/games/${gameId}/snapshot/${snapshotId}/restore-file`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ relPath })
    });
    if (res.ok) {
      showToast(`Successfully restored "${relPath}"!`, 'success');
      closeModal(document.getElementById('snapshot-browser-modal'));
    } else {
      const err = await res.json();
      showToast(`Failed: ${err.error}`, 'error');
    }
  } catch (err) {
    showToast(`Error: ${err.message}`, 'error');
  }
};

async function openCloudExplorer(gameId) {
  const game = appState.games[gameId];
  if (!game) return;

  document.getElementById('cloud-explorer-title').textContent = `☁️ Cloud Backup Explorer - ${game.name}`;
  openModal(cloudExplorerModal);

  // Show loading state
  cloudExplorerLoading.classList.remove('hidden');
  cloudExplorerEmpty.classList.add('hidden');
  cloudExplorerContent.classList.add('hidden');
  cloudExplorerTableBody.innerHTML = '';

  try {
    const res = await fetch(`/api/cloud/snapshots/${gameId}`);
    if (!res.ok) {
      const err = await res.json();
      throw new Error(err.error || 'Failed to fetch cloud snapshots.');
    }
    const data = await res.json();
    const snapshots = data.snapshots || [];

    cloudExplorerLoading.classList.add('hidden');

    if (snapshots.length === 0) {
      cloudExplorerEmpty.classList.remove('hidden');
      return;
    }

    cloudExplorerContent.classList.remove('hidden');
    snapshots.forEach(snap => {
      const tr = document.createElement('tr');
      tr.style.borderBottom = '1px solid var(--border)';
      
      const dateStr = new Date(snap.timestamp).toLocaleString();
      const sizeStr = (snap.sizeBytes / 1024).toFixed(1) + ' KB';

      tr.innerHTML = `
        <td style="padding: 10px 14px;"><span class="badge" style="background: rgba(6,182,212,0.1); color: var(--cyan); border: 1px solid rgba(6,182,212,0.2); font-family: var(--font-mono);">${snap.branch}</span></td>
        <td style="padding: 10px 14px; color: var(--text-2); font-family: var(--font-mono); font-size: 11px;">${dateStr}</td>
        <td style="padding: 10px 14px; color: var(--text-3); font-family: var(--font-mono);">${sizeStr}</td>
        <td style="padding: 10px 14px; text-align: right;">
          <button class="btn-primary btn-sm" onclick="restoreCloudSnapshot('${gameId}', '${snap.id}', '${snap.remoteName}')">Restore</button>
        </td>
      `;
      cloudExplorerTableBody.appendChild(tr);
    });
  } catch (err) {
    cloudExplorerLoading.classList.add('hidden');
    cloudExplorerTableBody.innerHTML = `<tr><td colspan="4" style="text-align:center; padding: 20px; color:var(--red);">Error: ${err.message}</td></tr>`;
    cloudExplorerContent.classList.remove('hidden');
  }
}

window.restoreCloudSnapshot = async (gameId, snapshotId, remoteName) => {
  if (!confirm(`Are you sure you want to download and restore cloud snapshot "${snapshotId}"?\nThis will overwrite your active save files.`)) {
    return;
  }

  closeModal(cloudExplorerModal);
  showToast(`Downloading and restoring cloud snapshot...`, 'info');

  try {
    const res = await fetch(`/api/cloud/restore/${gameId}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ remoteName, snapshotId })
    });

    if (res.ok) {
      showToast('Successfully restored cloud snapshot!', 'success');
      // Refresh details drawer if active
      if (activeGameId === gameId) {
        renderDrawerDetails();
      }
    } else {
      const err = await res.json();
      showToast(`Restore failed: ${err.error}`, 'error');
    }
  } catch (err) {
    showToast(`Error restoring snapshot: ${err.message}`, 'error');
  }
};

// ============================================================
// BOOT
// ============================================================
window.addEventListener('DOMContentLoaded', initApp);
