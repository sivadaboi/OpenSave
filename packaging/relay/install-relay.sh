#!/usr/bin/env bash
#
# One-command relay install for a Linux server.
#
# Setting up a relay was: build or fetch a binary, work out where to put it,
# write a systemd unit, open a port, and — if you want the encryption the
# clients default to — put a reverse proxy in front with the two WebSocket
# settings that are easy to miss. Every one of those is the same on every
# machine, which makes it a script's job rather than a person's.
#
#   curl -fsSL https://raw.githubusercontent.com/Liquid-co/OpenSave/main/packaging/relay/install-relay.sh | sudo bash
#   sudo ./install-relay.sh --domain relay.example.com    # with automatic TLS
#   sudo ./install-relay.sh --uninstall
#
# What it will not do, because it cannot: point a DNS record at this machine.
# TLS needs a name that resolves here, and only your registrar can do that.
# Without --domain the relay still works, over unencrypted ws://.

set -euo pipefail

REPO="Liquid-co/OpenSave"
BIN_PATH="/usr/local/bin/opensave-relay"
UNIT_PATH="/etc/systemd/system/opensave-relay.service"
SERVICE_USER="opensave-relay"
PORT="8386"
DOMAIN=""
VERSION=""
UNINSTALL=0
SKIP_VERIFY=0
DRY=0

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m !\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# Everything that changes the machine goes through run() or writes via
# write_file(), so --dry-run can show the whole plan without touching
# anything. Worth the indirection for a script people are asked to pipe into
# a root shell: it means "read it first" is something you can actually do, on
# your own machine, in the state it is really in.
run() {
	if [ "$DRY" -eq 1 ]; then
		printf '   \033[2mwould run:\033[0m %s\n' "$*"
	else
		"$@"
	fi
}

# write_file <path> — content on stdin.
write_file() {
	if [ "$DRY" -eq 1 ]; then
		printf '   \033[2mwould write %s:\033[0m\n' "$1"
		sed 's/^/     | /'
	else
		cat >"$1"
	fi
}

usage() {
	cat <<'EOF'
opensave-relay installer

  --domain <host>   Serve over https/wss at this name, with a certificate
                    obtained automatically. The name must already resolve to
                    this machine.
  --port <n>        Port the relay listens on (default 8386). With --domain
                    this stays internal and only 443 is public.
  --version <tag>   Install a specific release (default: the latest).
  --skip-verify     Do not check the download against SHA256SUMS. Only if the
                    release genuinely has no checksums file.
  --uninstall       Stop and remove the service, the binary and the user.
  --dry-run         Print every change it would make, and make none. Needs no
                    root, and is the way to read this before trusting it.
  --help            This.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--domain) DOMAIN="${2:-}"; shift 2 ;;
		--port) PORT="${2:-}"; shift 2 ;;
		--version) VERSION="${2:-}"; shift 2 ;;
		--skip-verify) SKIP_VERIFY=1; shift ;;
		--uninstall) UNINSTALL=1; shift ;;
		--dry-run) DRY=1; shift ;;
		--help|-h) usage; exit 0 ;;
		*) die "unknown option: $1 (try --help)" ;;
	esac
done

if [ "$DRY" -eq 0 ] && [ "$(id -u)" -ne 0 ]; then
	die "run this as root: sudo $0 $*
Or see what it would do first, as yourself:  $0 --dry-run"
fi

if ! command -v systemctl >/dev/null 2>&1; then
	if [ "$DRY" -eq 1 ]; then
		# A dry run is for reading the plan, and should work anywhere.
		warn "no systemd here — showing the plan anyway, since this is a dry run"
	else
		die "no systemd here, so there is nothing to install into.

Run the relay directly instead:
  PORT=$PORT opensave-relay
Or as a container:
  docker run -d --restart unless-stopped -p $PORT:10000 opensave-relay"
	fi
fi

# ── Uninstall ────────────────────────────────────────────────────────
if [ "$UNINSTALL" -eq 1 ]; then
	say "Stopping and removing the relay"
	run systemctl disable --now opensave-relay 2>/dev/null || true
	run rm -f "$UNIT_PATH" "$BIN_PATH"
	run systemctl daemon-reload
	run userdel "$SERVICE_USER" 2>/dev/null || true
	say "Gone. Any Caddy config was left alone — remove the OpenSave block by hand if you added one."
	exit 0
fi

# ── Which build ──────────────────────────────────────────────────────
case "$(uname -m)" in
	x86_64|amd64)  ASSET="opensave-linux-amd64.tar.gz"; INNER="opensave-linux/opensave-relay" ;;
	aarch64|arm64) ASSET="opensave-linux-arm64.tar.gz"; INNER="opensave-linux-arm64/opensave-relay" ;;
	*) die "no prebuilt relay for $(uname -m). Build it yourself: go build ./cmd/opensave-relay" ;;
esac

for tool in curl tar; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required but not installed"
done

if [ -z "$VERSION" ]; then
	say "Finding the latest release"
	VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
	[ -n "$VERSION" ] || die "could not work out the latest version — pass --version <tag>"
fi
say "Installing $VERSION ($ASSET)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BASE="https://github.com/$REPO/releases/download/$VERSION"

curl -fsSL "$BASE/$ASSET" -o "$TMP/$ASSET" || die "download failed: $BASE/$ASSET"

# Verified by default. This script runs as root and puts a binary in your
# PATH; taking the download on trust would be the weakest link in the whole
# arrangement.
if [ "$SKIP_VERIFY" -eq 0 ]; then
	if curl -fsSL "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS" 2>/dev/null; then
		WANT="$(awk -v f="$ASSET" '$2 == f || $2 == "*"f {print $1}' "$TMP/SHA256SUMS" | head -1)"
		if [ -n "$WANT" ]; then
			GOT="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
			[ "$WANT" = "$GOT" ] || die "checksum mismatch for $ASSET
  expected $WANT
  got      $GOT
Refusing to install. Try again, and if it persists say so on the issue tracker."
			say "Checksum verified"
		else
			warn "$ASSET is not listed in SHA256SUMS; continuing unverified"
		fi
	else
		warn "no SHA256SUMS published for $VERSION; continuing unverified"
	fi
fi

tar -xzf "$TMP/$ASSET" -C "$TMP" "$INNER" 2>/dev/null ||
	die "$ASSET did not contain $INNER — wrong release layout?"

run install -m 0755 "$TMP/$INNER" "$BIN_PATH"
if [ "$DRY" -eq 0 ]; then
	# Reports the tag we asked for rather than running the binary to ask it.
	#
	# Running it hung the installer outright. Every relay before the fix that
	# taught it about arguments ignored them and started a server instead —
	# so `opensave-relay --version` never returned, and the command
	# substitution here waited on it forever, with the unit file unwritten and
	# a stray relay left listening. Found by installing v2.2.1 on a real
	# machine, which is the version anyone running this today would get.
	#
	# It is also simply better: this executes a binary downloaded seconds ago,
	# as root, to learn something already known.
	say "Installed $BIN_PATH ($VERSION)"
fi

# ── Service ──────────────────────────────────────────────────────────
id -u "$SERVICE_USER" >/dev/null 2>&1 ||
	run useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"

# Hardened because it is a network-facing daemon that needs nothing from this
# machine: no files, no home, no privileges. If it is ever compromised the
# blast radius should be the socket and nothing else.
write_file "$UNIT_PATH" <<EOF
[Unit]
Description=OpenSave WAN relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Environment=PORT=$PORT
ExecStart=$BIN_PATH
Restart=always
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6
MemoryMax=512M

[Install]
WantedBy=multi-user.target
EOF

run systemctl daemon-reload
run systemctl enable --now opensave-relay
if [ "$DRY" -eq 0 ]; then sleep 1; fi
if [ "$DRY" -eq 0 ]; then
	systemctl is-active --quiet opensave-relay ||
		die "the service did not start. Look at: journalctl -u opensave-relay -n 40"
	say "Service running"
fi

# ── TLS, if a name was given ─────────────────────────────────────────
PUBLIC_URL="ws://$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || echo 'YOUR-SERVER-IP'):$PORT"

if [ -n "$DOMAIN" ]; then
	if [ "$DRY" -eq 1 ] && ! command -v caddy >/dev/null 2>&1; then
		say "Would install Caddy for automatic certificates, then serve $DOMAIN"
	elif ! command -v caddy >/dev/null 2>&1; then
		say "Installing Caddy for automatic certificates"
		if command -v apt-get >/dev/null 2>&1; then
			apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg >/dev/null
			curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' |
				gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
			curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
				>/etc/apt/sources.list.d/caddy-stable.list
			apt-get update -qq && apt-get install -y caddy >/dev/null
		elif command -v dnf >/dev/null 2>&1; then
			dnf install -y 'dnf-command(copr)' >/dev/null
			dnf copr enable -y @caddy/caddy >/dev/null
			dnf install -y caddy >/dev/null
		else
			die "could not install Caddy automatically on this distribution.
Install it yourself, then add:

  $DOMAIN {
      reverse_proxy localhost:$PORT
  }"
		fi
	fi

	# Appended, not overwritten: this machine may already be serving something.
	if ! grep -q "^$DOMAIN" /etc/caddy/Caddyfile 2>/dev/null; then
		if [ "$DRY" -eq 1 ]; then
			printf '   \033[2mwould append to /etc/caddy/Caddyfile:\033[0m %s { reverse_proxy localhost:%s }\n' \
				"$DOMAIN" "$PORT"
		else
			printf '\n%s {\n    reverse_proxy localhost:%s\n}\n' "$DOMAIN" "$PORT" >>/etc/caddy/Caddyfile
		fi
	fi
	run systemctl reload caddy 2>/dev/null || run systemctl restart caddy
	PUBLIC_URL="wss://$DOMAIN"
	say "Caddy serving $DOMAIN — the certificate arrives on the first request"
fi

# ── Firewall ─────────────────────────────────────────────────────────
# Only the port the outside actually needs: with a proxy in front the relay
# port stays local.
OPEN_PORT="$PORT"
[ -n "$DOMAIN" ] && OPEN_PORT="443"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
	run ufw allow "$OPEN_PORT/tcp" && say "Opened $OPEN_PORT/tcp in ufw"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
	run firewall-cmd --permanent --add-port="$OPEN_PORT/tcp"
	run firewall-cmd --reload
	say "Opened $OPEN_PORT/tcp in firewalld"
else
	warn "No active ufw/firewalld found. If your provider has its own firewall, open $OPEN_PORT/tcp there."
fi

# ── What to do next ──────────────────────────────────────────────────
HEALTH="$(printf '%s' "$PUBLIC_URL" | sed 's|^wss://|https://|; s|^ws://|http://|')/health"
# A bounded read rather than `tr </dev/urandom | head`: head closing the pipe
# early kills tr with SIGPIPE, the || fallback fires on top of the value that
# was already produced, and you get "f64rw3pick-your-own" printed as the code
# to type. Take a fixed number of bytes and filter what comes out.
ROOM="$(head -c 64 /dev/urandom 2>/dev/null | tr -dc 'a-z0-9' | cut -c1-6)"
[ -n "$ROOM" ] || ROOM="pick-your-own"

cat <<EOF

$(say "Done.")

  Relay URL   $PUBLIC_URL
  Health      $HEALTH
  Logs        journalctl -u opensave-relay -f

Now set this up on each device whose saves you sync — NOT on this server.
The relay never joins a room; rooms exist because clients ask for them.

  opensave config set relay-url $PUBLIC_URL
  opensave relay join $ROOM

Use the same room code on every device. In the app it is the same two fields
under Internet Sync. Treat the code like a password: anyone who has it can
send your devices a pairing request, though they still cannot sync anything
without you approving it on the device.

EOF

if [ -z "$DOMAIN" ]; then
	warn "This relay is unencrypted (ws://). Fine on a network you trust; across the
    internet, re-run with --domain relay.example.com once a DNS record points here."
fi
