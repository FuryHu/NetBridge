# NetBridge

**English** | [简体中文](README.zh-CN.md) | [日本語](README.ja.md)

A lightweight virtual-LAN tool for playing LAN-only games (or using any LAN software) with friends over the internet. It builds a private virtual network over UDP, preferring direct P2P connections with NAT traversal and falling back to a server relay when punching fails.

> Built for the classic "my friend is behind a router and the LAN lobby is empty" problem - e.g. Warcraft III, Red Alert 2, Age of Empires, StarCraft, Civilization VI, and other LAN multiplayer games.

## ✨ Features

- **Virtual LAN** - each room gets a `10.66.0.0/16` subnet; peers get stable virtual IPs (reused across restarts) and appear to be on the same switch.
- **P2P first, relay fallback** - UDP hole punching on both IPv4 and IPv6 in parallel; falls back to server relay after 3s. The UI shows each peer's channel (P2P / relay) and protocol family (IPv4/IPv6).
- **LAN discovery friendly** - broadcast/multicast are distributed to all room members, so game lobby scans just work.
- **Room chat & voice** - in-room text chat and voice over the same P2P/relay channel. Opus via WebCodecs (raw PCM fallback), echo cancellation, per-peer volume & mute.
- **Localized UI** - Chinese, English, Japanese; switch from the top bar (persists across launches).
- **Single-binary server** - pure Go, no runtime deps, runs on a small Linux VPS behind systemd.

## 🏗️ Architecture

A Go workspace of three modules:

| Module | Role |
| --- | --- |
| `protocol/` | Shared wire protocol - JSON signaling + compact binary data frames, codec, constants. |
| `server/` | UDP signaling + relay server. Allocates VIPs, tracks rooms/peers, broadcasts endpoints to assist punching, relays when P2P fails. Pure Go. |
| `client/` | Desktop app ([Wails v2](https://wails.io) = Go + Vue 3). Manages the NIC via [wintun](https://www.wintun.net/) on Windows and bridges IP packets between TUN and the network layer. |

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

- **Go** 1.26 · **Wails** v2.12 · **Vue 3** + **Vite** 3
- **wintun** (Windows virtual NIC) · **google/uuid**
- The server has no third-party deps beyond the shared `protocol` module.

## 🚀 Quick Start

### Server

Deploy on a VPS with UDP `10555` open:

```bash
cd server
go run .                                  # default: listen on [::]:10555 (dual-stack)
go build -o netbridge-server . && ./netbridge-server   # or build & run
```

Optional flags / env:

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `-addr` | - | `[::]:<port>` | Listen address, e.g. `0.0.0.0:10555` (v4 only) |
| `-port` | `NETBRIDGE_PORT` | `10555` | UDP listen port |
| `-room-timeout` | `NETBRIDGE_ROOM_TIMEOUT` | `15` | Peer offline timeout (s) |

Logs go to stdout as structured text (journald under systemd).

### Client (Windows)

Requires the [Wails CLI](https://wails.io/docs/gettingstarted/installation) and Go:

```bash
cd client
wails dev      # live development (hot reload)
wails build    # produce a redistributable .exe / NSIS installer
```

> Creating the wintun NIC needs **administrator privileges** - the manifest declares `requireAdministrator`; `wails dev` has a `RestartAsAdmin` fallback.

Usage: launch -> enter the server address (`vps-ip:10555`) -> join a room with a nickname -> share the room name. The virtual NIC comes up automatically once the server assigns you a VIP.

## ⚙️ How It Works

The server assigns a VIP and broadcasts everyone's public v4/v6 endpoints. Clients punch both paths in parallel - the first to connect becomes the P2P channel; if none connects within 3s, the server relays (routing by VIP, never inspecting payload). A TUN bridge moves raw IP packets between the virtual NIC and the P2P/relay channel.

Key constants (`protocol/constants.go`): subnet `10.66.0.0/16`, MTU `1400`, heartbeat `5s`, peer timeout `15s`, punch timeout `3s`, port `10555`.

## 📝 Notes

- Client is Windows-only (wintun); the server runs anywhere Go does.
- Open UDP `10555` on the server for both signaling and relay.
- P2P success depends on NAT type; symmetric NATs typically fall back to relay.

## 📄 License

Released under the [MIT License](LICENSE). Copyright © 2026 FuryHu.
