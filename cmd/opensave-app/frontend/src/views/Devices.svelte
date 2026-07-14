<script>
  import { peers, discoveredPeers, wanRoom, appUpdate, toast, askConfirm } from '../lib/stores.js';
  import { api, native } from '../lib/api.js';
  import InternetSync from './InternetSync.svelte';

  export let params = {};

  let manualIp = '';
  let manualPort = 8383;
  let busy = false;
  // Which "add a device" method is shown. Deep-links can request the
  // internet tab; otherwise default to the local network.
  let connectTab = params.tab === 'internet' ? 'wan' : 'lan';

  $: pairedList = Object.values($peers).sort((a, b) => a.name.localeCompare(b.name));
  $: pairedIds = new Set(pairedList.map((p) => p.id));
  $: lanDiscovered = $discoveredPeers.filter((d) => !d.isWan && !pairedIds.has(d.id));

  async function run(fn, okMsg) {
    if (busy) return;
    busy = true;
    try {
      await fn();
      if (okMsg) toast(okMsg, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  const pairDiscovered = (d) =>
    run(() => api.post('/api/peers/pair', { address: d.address, port: d.port }), `Pairing request sent to ${d.deviceName}`);
  const pairManual = () =>
    run(() => api.post('/api/peers/pair', { address: manualIp, port: Number(manualPort) }), 'Pairing request sent');
  const unpair = async (peer) => {
    if (!(await askConfirm(`Unpair "${peer.name}"? Their games stay on both devices; syncing between you stops.`, { title: 'Unpair device?', confirmText: 'Unpair', danger: true }))) return;
    run(() => api.del(`/api/peers/${peer.id}`), `Unpaired ${peer.name}`);
  };

  // Peer-to-peer app update: pull the newer build the peer is running and
  // install it here — no manually copying the exe between machines.
  const updateFromPeer = async (peer) => {
    const ok = await askConfirm(
      `${peer.name} is running a newer OpenSave build (${peer.appVersion}). Download it from ${peer.name} and update this device? OpenSave restarts itself when done.`,
      { title: 'Update OpenSave?', confirmText: 'Update & restart' }
    );
    if (!ok) return;
    const err = await native.installFromPeer(peer.id);
    if (err) toast(err, 'error');
  };

  const fmtTime = (t) => (t ? new Date(t).toLocaleString() : 'never');
</script>

<div class="head">
  <h2 class="page-title">Devices</h2>
</div>

<h3 class="section">Paired devices</h3>
{#if pairedList.length === 0}
  <div class="empty">
    <h3>No paired devices</h3>
    <p>Pair a device below — on your Wi-Fi it appears automatically, or connect over the internet with a room code.</p>
  </div>
{:else}
  <div class="list">
    {#each pairedList as peer (peer.id)}
      <div class="card peer">
        <div class="peer-icon">{peer.deviceType === 'deck' ? '🎮' : '🖥️'}</div>
        <div class="peer-info">
          <div class="peer-name">
            {peer.name}
            <span class="badge" class:online={peer.status === 'online'} class:offline={peer.status !== 'online'}>
              {peer.status}
            </span>
          </div>
          <div class="peer-meta">
            {peer.address === 'relay' ? '🌐 internet relay' : `🖧 ${peer.address}:${peer.port}`}
            · last synced {fmtTime(peer.lastSynced)}
            {#if peer.appVersion}· OpenSave {peer.appVersion}{/if}
          </div>
        </div>
        {#if peer.hasNewerBuild && peer.status === 'online'}
          <button class="btn small primary" disabled={busy || !!$appUpdate} on:click={() => updateFromPeer(peer)}>
            ⬆ Update from this device
          </button>
        {/if}
        <button class="btn small danger" disabled={busy} on:click={() => unpair(peer)}>Unpair</button>
      </div>
    {/each}
  </div>
{/if}

<h3 class="section">Add a device</h3>
<div class="pill-tabs connect-tabs">
  <button class:active={connectTab === 'lan'} on:click={() => (connectTab = 'lan')}>🖧 On this network</button>
  <button class:active={connectTab === 'wan'} on:click={() => (connectTab = 'wan')}>
    🌐 Over the internet
    {#if $wanRoom?.connected}<span class="tab-dot"></span>{/if}
  </button>
</div>

{#if connectTab === 'lan'}
  <p class="quiet lan-intro">
    Devices running OpenSave on the same Wi-Fi/Ethernet discover each other automatically. No setup needed.
  </p>
  <h4 class="subsection">Found on your network</h4>
  {#if lanDiscovered.length === 0}
    <p class="quiet">No unpaired devices found on the local network. Make sure OpenSave is running on the other device.</p>
  {:else}
    <div class="list">
      {#each lanDiscovered as d (d.id)}
        <div class="card peer">
          <div class="peer-icon">{d.deviceType === 'deck' ? '🎮' : '🖥️'}</div>
          <div class="peer-info">
            <div class="peer-name">{d.deviceName}</div>
            <div class="peer-meta">{d.address}:{d.port}</div>
          </div>
          <button class="btn small primary" disabled={busy} on:click={() => pairDiscovered(d)}>Pair</button>
        </div>
      {/each}
    </div>
  {/if}

  <h4 class="subsection">Add by IP address</h4>
  <div class="card manual">
    <input placeholder="192.168.1.42" bind:value={manualIp} />
    <input class="port" type="number" bind:value={manualPort} />
    <button class="btn primary" disabled={!manualIp || busy} on:click={pairManual}>Send pairing request</button>
  </div>
{:else}
  <InternetSync />
{/if}

<style>
  .head {
    margin-bottom: 20px;
  }
  .section {
    margin: 22px 0 10px;
  }
  .subsection {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--text-dim);
    margin: 18px 0 8px;
  }
  .connect-tabs {
    margin-bottom: 14px;
  }
  .connect-tabs button {
    display: inline-flex;
    align-items: center;
    gap: 7px;
  }
  .tab-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--success);
    box-shadow: 0 0 6px rgba(74, 222, 128, 0.7);
  }
  .lan-intro {
    margin-bottom: 4px;
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .peer {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
  }
  .peer-icon {
    font-size: 1.3rem;
  }
  .peer-info {
    flex: 1;
    min-width: 0;
  }
  .peer-name {
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .peer-meta {
    font-size: 0.78rem;
    color: var(--text-faint);
    margin-top: 2px;
  }
  .quiet {
    color: var(--text-faint);
    font-size: 0.88rem;
  }
  .manual {
    display: flex;
    gap: 10px;
    padding: 14px;
  }
  .manual input {
    padding: 8px 12px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
    flex: 1;
  }
  .manual .port {
    flex: 0 0 90px;
  }
</style>
