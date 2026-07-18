# NetBridge

**English** | [简体中文](README.zh-CN.md)

A lightweight virtual-LAN tool that lets you play LAN-only games (and use any LAN software) with friends over the internet. It builds a private virtual network on top of UDP, preferring direct peer-to-peer connections with NAT traversal and falling back to a server relay when punching fails.

> Built for the classic "my friend is behind a router and the LAN lobby is empty" problem - e.g. Civilization VI LAN multiplayer.

## ✨ Features

- **Virtual LAN** - each room gets a `10.66.0.0/16` subnet; peers are assigned virtual IPs (`10.66.0.2`, `10.66.0.3`, …) and appear to each other as if on the same switch.
- **P2P first, relay fallback** - UDP hole punching with a dual-stack twist: it punches both IPv4 *and* IPv6 endpoints in parallel and uses whichever connects first. If punching times out (3s), traffic transparently relays through the server.
- **Stable virtual IP** - each machine holds a persistent PeerID; the server reuses the same VIP across restarts and re-joins, so "your IP" never drifts.
- **LAN discovery friendly** - limited/subnet broadcast (`255.255.255.255`, `10.66.255.255`) and multicast (`224.0.0.0/4`) are distributed to all room members, so game lobby scans "just work".
- **Room chat** - in-room text chat broadcast through the server.
- **Voice chat** - in-room voice over the same P2P/relay channel. Opus via WebCodecs (falls back to raw PCM on older WebView2), browser echo cancellation/AGC, mic toggle + per-peer volume + mute. Voice frames reuse the compact binary format (`FrameVoice`), relayed transparently by the server.
- **Latency visibility** - heartbeat-based keep-alive with RTT measurement; the UI shows each peer's channel (P2P / relay / none) and protocol family (IPv4/IPv6).
- **Compact binary data frames** - signaling stays JSON for debuggability, but the high-throughput data path uses a 12-byte binary header to avoid the ~35% bloat of JSON+base64 that would blow the 1400 MTU and cause fragmentation loss.
- **Single-binary server** - pure Go, no runtime deps, runs fine on a small Linux VPS behind systemd.

## 🏗️ Architecture

NetBridge is a Go workspace of three modules:

| Module | Role |
| --- | --- |
| `protocol/` | Shared wire protocol - JSON signaling packets + compact binary data frames, codec, constants. |
| `server/` | UDP signaling + relay server. Allocates virtual IPs, tracks rooms/peers, broadcasts peer endpoints to assist punching, relays data when P2P fails. Pure Go. |
| `client/` | Desktop app ([Wails v2](https://wails.io) = Go + Vue 3). Manages the virtual NIC via [wintun](https://www.wintun.net/) on Windows, bridges IP packets between the TUN device and the network layer, and drives the UI. |

```
        ┌───────────────┐         UDP :10555         ┌───────────────┐
        │   Client A    │ ◄════ signaling / relay ═══►│   Client B    │
        │  (Wails+TUN)  │                            │  (Wails+TUN)  │
        └───────┬───────┘                            └───────┬───────┘
                │ hole-punch (v4 & v6 in parallel)          │
                └══════════════ P2P game data ══════════════┘
                          (falls back to server relay)
                              ▲
                              │
                       ┌──────┴──────┐
                       │   Server    │
                       │  (pure Go)  │
                       └─────────────┘
```

## 🧱 Tech Stack

- **Go** 1.26
- **Wails** v2.12 (desktop shell) · **Vue 3** + **Vite** 3 (frontend)
- **wintun** (Windows virtual NIC) · **google/uuid**
- Server has no third-party deps beyond the shared `protocol` module.

## 🚀 Quick Start

### Server

The server is a single Go binary - deploy it on a VPS with UDP `10555` open.

```bash
cd server
go run .                          # default: listen on [::]:10555 (dual-stack)

# or build & run
go build -o netbridge-server .
./netbridge-server -addr 0.0.0.0:10555 -port 10555
```

Flags & env (all optional):

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `-addr` | - | `[::]:<port>` | Listen address, e.g. `0.0.0.0:10555` (v4 only) |
| `-port` | `NETBRIDGE_PORT` | `10555` | UDP listen port |
| `-room-timeout` | `NETBRIDGE_ROOM_TIMEOUT` | `15` | Peer offline timeout in seconds |

Logs go to stdout as structured text - under systemd they're picked up by journald.

### Client (Windows)

Requires the [Wails CLI](https://wails.io/docs/gettingstarted/installation) and Go.

```bash
cd client
wails dev      # live development (hot reload)
wails build    # produce a redistributable .exe / NSIS installer
```

> The client needs **administrator privileges** to create the wintun virtual NIC - the Windows manifest already declares `requireAdministrator`. In `wails dev` (where the parent process isn't elevated) there's a `RestartAsAdmin` fallback.

To use it: launch the app -> enter the server address (`vps-ip:10555`) -> join a room with a nickname -> share the room name with friends. The virtual NIC comes up automatically once the server assigns you a VIP.

## ⚙️ How It Works (briefly)

1. **Join** - client sends `join_room`; server assigns the next free VIP and replies with `room_status` (full member list + your VIP).
2. **Discover** - server broadcasts each peer's public v4/v6 endpoints (`peer_address`) so everyone can attempt punching.
3. **Punch** - clients exchange punch packets over both v4 and v6 in parallel; the first path that connects becomes the P2P channel.
4. **Relay** - if no path connects within `PunchTimeout` (3s), data is wrapped in a compact `Relay` frame and forwarded by the server (which only routes by destination VIP, never inspecting payload).
5. **Bridge** - the TUN bridge reads raw IP packets off the virtual NIC, resolves the destination VIP, and sends them P2P (or via relay); inbound packets are written straight back to the NIC.

Key constants (`protocol/constants.go`): virtual subnet `10.66.0.0/16`, MTU `1400`, heartbeat `5s`, peer timeout `15s`, punch timeout `3s`, default port `10555`.

## 📁 Project Structure

```
NetBridge/
├── protocol/            # shared wire protocol (codec, frames, packet types)
│   ├── frame.go         # 12-byte compact binary data frame
│   ├── voice.go         # voice payload sub-format (codec/seq/timestamp/audio)
│   ├── packet.go        # JSON signaling packet envelope + types
│   └── constants.go     # ports, VIP prefix, MTU, timeouts
├── server/              # UDP signaling + relay server (pure Go)
│   ├── internal/
│   │   ├── room/        # room & peer management, VIP allocation
│   │   ├── relay/       # relay routing (zero-copy frame forwarding)
│   │   ├── signaling/   # join/status/leave/chat handlers
│   │   └── server/      # UDP server loop + dispatch
│   └── main.go
└── client/              # Wails desktop app
    ├── app.go           # Wails bindings (frontend ↔ backend)
    ├── core/            # client core: connection, room, chat, channels
    ├── tun/             # wintun adapter + TUN↔network bridge
    ├── netconn/         # UDP conn, hole punching
    ├── peer/            # peer manager
    ├── frontend/        # Vue 3 + Vite UI (incl. src/voice/: WebCodecs Opus + AudioWorklet capture/playback)
    └── build/           # Windows icon, NSIS installer, wintun.dll
```

## 📝 Notes

- Currently Windows-only on the client side (wintun). The server runs anywhere Go does.
- UDP `10555` must be open on the server for both signaling and relay.
- P2P success depends on NAT types; symmetric NATs typically fall back to relay.
- Voice chat reuses the same P2P/relay channel; the server relays voice frames transparently without inspecting payload.

## 📄 License

Personal project. See source for authorship.
