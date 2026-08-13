# Running your own relay

Two devices on the same network find each other by themselves. On different
networks — your PC at home and a laptop somewhere else — they need something
with a public address to introduce them. That is the relay.

OpenSave ships pointed at a free hosted one, so **you do not need this guide
to use internet sync**. Run your own if you want to not depend on someone
else's server, or want it to wake instantly instead of cold-starting.

---

## The one thing to get straight first

**The relay never joins a room, and there is no command to make it.**

This is the most common confusion, so it is worth being blunt about. The relay
is a dumb pipe. It has no configuration beyond a port, no idea which room codes
exist until clients turn up, and nothing to sign in to. There is no
`opensave relay join` to run *on the relay* — that command belongs on your
**gaming devices**, and there is nothing to install on the server beyond the
relay binary itself.

What actually happens:

1. Your PC connects to the relay and says "put me in room `purple-otter-42`".
2. The relay makes that room exist, because someone asked for it.
3. Your laptop connects and asks for the same room.
4. The relay now forwards encrypted frames between the two. It cannot read
   them, and stores nothing.

So: **run the container, then point your devices at it.** That is the whole job.
If your VPS is not itself a machine you play games on, it never joins a room.

---

## The short way

On a Linux server, one command does the lot — binary, systemd service,
firewall, and a certificate if you have a domain pointed here:

```bash
curl -fsSL https://raw.githubusercontent.com/Liquid-co/OpenSave/main/packaging/relay/install-relay.sh -o install-relay.sh
sudo bash install-relay.sh --domain relay.example.com
```

Leave `--domain` off and the relay speaks unencrypted `ws://`, which OpenSave
accepts only at a private address — a LAN or a VPN. On a hosted server, whose
only address is public, the installer will say so and print no address to copy,
because anything it printed would be refused. See [Why `ws://` is not enough](#3-put-tls-in-front-of-it).

With a name and a certificate it prints the relay URL and the two commands to
run on your gaming devices; `sudo bash install-relay.sh --uninstall` removes
everything.

**Read it before you run it as root.** It has a `--dry-run` that needs no
privileges and prints every change it would make, including the systemd unit
in full:

```bash
bash install-relay.sh --dry-run --domain relay.example.com
```

The download is checked against the release's `SHA256SUMS` before anything is
installed.

The rest of this page is the same thing done by hand, and what to do when it
does not work.

## 1. Run the relay

### Docker

```bash
docker build -f relay/Dockerfile -t opensave-relay .
docker run -d --name opensave-relay --restart unless-stopped -p 8386:10000 opensave-relay
```

The image sets `PORT=10000` internally; the `-p` maps it to 8386 on the host.
Use whatever host port you like.

### Binary

```bash
go build -o opensave-relay ./cmd/opensave-relay
./opensave-relay
```

It prints where it is listening and stays in the foreground. For a permanent
install, run it under systemd.

### Settings

Both forms are configured with environment variables — there are no flags:

| Variable | Default | What it does |
| --- | --- | --- |
| `PORT` | `8386` (`10000` in the image) | Port to listen on |
| `MAX_PER_ROOM` | `20` | Most devices allowed in one room |
| `GOOGLE_DRIVE_CLIENT_SECRET` | unset | Only for the optional OAuth token proxy. Leave it alone unless you know you need it |

`opensave-relay --help` prints the same list. It takes **no commands** — if you
give it one it says so and exits rather than starting a server that ignored
half its command line. In particular `relay-url` is a *client* setting and does
nothing here; see step 4.

## 2. Check it is up

```bash
curl http://your-server:8386/health
```

You should get JSON with `"status":"ok"`, plus `rooms`, `clients`,
`totalConnections` and `totalMessages`. Straight after starting, `rooms` is
**0** — that is correct, and does not mean anything is wrong. Rooms appear
when devices connect.

## 3. Put TLS in front of it

Not optional across the internet, and the app enforces it. Nothing in OpenSave
encrypts the sync payload — the save file travels gzipped inside JSON — so the
relay connection is the only thing between a save and the network it crosses.
A bare relay speaks plain `ws://`, and the app refuses that to any public
address, accepting it only where the network is itself the trust boundary
(loopback, a home LAN, or a private overlay such as Tailscale).

The usual arrangement is a reverse proxy — Caddy, nginx, Traefik — terminating
TLS on 443 and forwarding to the relay. Caddy needs two lines:

```
relay.example.com {
    reverse_proxy localhost:8386
}
```

Caddy gets a certificate automatically, and your relay URL becomes
`wss://relay.example.com`.

**Whatever proxy you use, it must pass WebSocket upgrades through.** Caddy and
Traefik do by default; nginx needs it spelled out:

```nginx
location / {
    proxy_pass http://localhost:8386;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;   # sync connections are long-lived
}
```

That last line matters: nginx closes idle connections after 60 seconds by
default, which shows up as a relay that keeps reconnecting.

Without a proxy you are limited to `ws://`, which the app accepts only at a
private address: `ws://192.168.1.50:8386` for a machine on your own network is
fine, and opening that port on the firewall is all it needs. A public address
over `ws://` is refused by the client, so a hosted server needs the proxy and
a certificate rather than an open port.

## 4. Point your devices at it

**On each device**, not on the server.

In the app: **Internet Sync → Relay server (self-hostable)**, put in your URL,
then **Test relay** to confirm it answers. Then either generate a room code or
paste the one you are already using.

From the command line:

```bash
opensave config set relay-url wss://relay.example.com
opensave relay join purple-otter-42     # the same code on every device
opensave relay status                   # shows the room and the relay in use
```

Provisioning devices rather than configuring them by hand? Set
`OPENSAVE_RELAY_URL` in the environment instead and skip the `config set` — it
overrides the stored value for as long as it is set, so a container gets the
right relay without anyone running a command inside it:

```bash
OPENSAVE_RELAY_URL=wss://relay.example.com opensave daemon start
```

Note the prefix: it is `OPENSAVE_RELAY_URL`, not a bare `RELAY_URL`, so it
cannot collide with something else on the same box and quietly redirect your
sync traffic.

Use the **same room code and the same relay URL on every device**. The code is
the only thing that decides who can find whom, so treat it like a password —
anyone who has it can send your devices a pairing request. They still cannot
sync anything without you approving the pairing on the device itself.

To check they have met:

```bash
opensave peers
```

To stop using internet sync on a device: `opensave relay leave`.

## If it does not connect

**`opensave relay status` shows the room but no peers appear.** Check the other
device has the *same* code and the *same* relay URL — a typo in either produces
exactly this, silently. `curl .../health` on both machines proves they can
reach the server at all.

**It connects and then drops every minute.** Your reverse proxy is timing out
the WebSocket. See `proxy_read_timeout` above.

**"The relay connection is down", retrying.** Either the URL is wrong (`wss://`
against a relay with no TLS, or `ws://` against one behind HTTPS), or the port
is not open. `curl` the health endpoint from the client machine — not from the
server — to tell those apart.

**Health check works, WebSocket does not.** The proxy is serving HTTP fine but
not passing upgrades. That is the `Upgrade`/`Connection` headers.

**Devices found each other but nothing syncs.** The relay's job is finished at
that point — this is a pairing or tracking problem, not a relay one. Check both
sides show as paired under **Devices**, and that both are tracking the game.

## What the relay can and cannot see

It forwards encrypted frames between devices in the same room. It does not hold
your saves, cannot read them, and keeps nothing after a device disconnects —
the room disappears when the last member leaves. Restarting it drops live
connections; clients reconnect on their own.

The health endpoint is public and reports counts only — how many rooms and
clients, never codes or contents.
