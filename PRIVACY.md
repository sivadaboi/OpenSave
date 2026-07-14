# Privacy

OpenSave is designed to be private by default. It is a self-contained tool
that runs entirely on your own devices.

## What OpenSave does not do

- **No accounts.** There is no sign-up, login, or user profile.
- **No telemetry.** OpenSave does not collect analytics, usage data, or crash
  reports, and does not phone home. The only outbound network request the app
  itself makes is an optional check of the GitHub Releases page for a newer
  version (this sends nothing about you).
- **No third-party save storage.** Your saves are never uploaded anywhere
  unless you explicitly enable Cloud Backup and choose a provider.

## Where your data lives

- **Saves & snapshots** stay on your own machine, in the folders you configure
  under Settings → Storage.
- **Peer-to-peer sync** transfers save data directly between your paired
  devices — over your LAN, or over the internet through a relay.
- **The relay** (whether the default public relay or one you self-host) only
  routes encrypted WebSocket frames between paired devices. It does not store
  your saves and cannot read their contents.

## Optional cloud backup

If you turn on Cloud Backup, snapshots are uploaded to the provider you choose
(Google Drive, Dropbox, OneDrive, WebDAV, a webhook, or a local/NAS folder)
using credentials you authorize. OAuth tokens are stored locally on your
device and are never sent to the OpenSave project. Disconnecting a provider
removes its stored tokens.

## Pairing & trust

Devices only sync after an explicit pairing approval. You can unpair a device
at any time from the Devices screen, which stops all sync with it.
