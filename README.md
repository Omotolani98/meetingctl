# meetingctl

Local-first meeting memory for ChatGPT via the Model Context Protocol (MCP).

`meetingd` captures meetings, stores encrypted transcript memory in SQLite, and exposes it through MCP. Raw audio is never streamed into ChatGPT.

## Architecture (v0.2)

```text
meetingctl  ──►  meetingd (loopback HTTP)
                    ├── capture / fixture ingest
                    ├── transcription (openai | whispercpp | command | fixture)
                    ├── analysis (openai | none | fixture)
                    ├── encrypted SQLite
                    └── MCP Streamable HTTP (/mcp)
                              │
                     Secure MCP Tunnel (optional)
                              │
                           ChatGPT
```

### Two credential modes (do not mix)

| Goal | What you need |
|------|----------------|
| **ChatGPT subscription** queries meeting memory | `meetingd` + Secure MCP Tunnel (`CONTROL_PLANE_API_KEY` for tunnel transport only) |
| **Autonomous STT / analysis** | `OPENAI_API_KEY` (Platform API billing — separate from ChatGPT Plus/Pro) |
| **Subscription-only capture** | Local STT (`whispercpp` or `command`) + MCP tunnel; no model API key |

ChatGPT cookies/session tokens are **not** supported and cannot fund API calls.

## Quick start (dev)

```bash
export MEETINGCTL_ENCRYPTION_KEY="$(go run ./cmd/meetingctl keygen)"
export MEETINGCTL_DATA_DIR="$PWD/.data"
export MEETINGCTL_TRANSCRIPTION_PROVIDER=fixture
export MEETINGCTL_ANALYSIS_PROVIDER=none

# Terminal 1
go run ./cmd/meetingd

# Terminal 2
go run ./cmd/meetingctl doctor
go run ./cmd/meetingctl start \
  --title "Platform Architecture Review" \
  --participants "Tolani,Sarah,Daniel" \
  --source fixture \
  --input testdata/platform-review

go run ./cmd/meetingctl status
go run ./cmd/meetingctl note "Daniel owns Redis migration"
go run ./cmd/meetingctl stop --input testdata/platform-review
go run ./cmd/meetingctl meetings
```

## Install (per-user agent)

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Omotolani98/meetingctl/main/scripts/install.sh | bash
```

Windows:

```powershell
irm https://raw.githubusercontent.com/Omotolani98/meetingctl/main/scripts/install.ps1 | iex
```

Installs `meetingctl`, `meetingd`, `meeting-mcp`, generates encryption key + control token, and starts a **per-user** login agent (LaunchAgent / systemd --user / Scheduled Task). Not a system service — desktop audio requires a user session.

## Providers

```bash
# OpenAI API (STT + analysis)
export OPENAI_API_KEY=sk-...
export MEETINGCTL_TRANSCRIPTION_PROVIDER=openai
export MEETINGCTL_TRANSCRIPTION_MODEL=gpt-4o-mini-transcribe
export MEETINGCTL_ANALYSIS_PROVIDER=openai
export MEETINGCTL_ANALYSIS_MODEL=gpt-4o-mini

# Local whisper.cpp
export MEETINGCTL_TRANSCRIPTION_PROVIDER=whispercpp
export MEETINGCTL_WHISPER_BINARY=$HOME/.meetingctl/bin/whisper-cli
export MEETINGCTL_WHISPER_MODEL=$HOME/.meetingctl/models/ggml-small.bin
export MEETINGCTL_ANALYSIS_PROVIDER=none

# Generic command STT (JSON stdin → JSONL stdout)
export MEETINGCTL_TRANSCRIPTION_PROVIDER=command
export MEETINGCTL_COMMAND_TRANSCRIBER=/path/to/stt-adapter
```

## Authentication

```bash
meetingctl auth                 # interactive
meetingctl auth status          # no secrets printed
meetingctl auth logout
meetingctl auth refresh-providers
```

Interactive menu:

1. **API Key** — choose a provider (OpenAI supported; others browsed from [models.dev](https://models.dev))
2. **ChatGPT Subscription** — connect MCP to ChatGPT via Secure Tunnel

API key (non-interactive):

```bash
printf '%s' "$OPENAI_API_KEY" | meetingctl auth --method api-key --provider openai --key-stdin
```

ChatGPT Subscription flow:

1. Ensures `meetingd` is healthy
2. Stores tunnel ID + Platform runtime key (transport only)
3. Configures `tunnel-client` when installed
4. Opens `https://chatgpt.com/plugins` for app connection

Credentials live under `~/.meetingctl/auth/` (mode `0600`). Secrets are never logged or returned by status APIs.

Stdio MCP (IDEs):

```bash
go run ./cmd/meeting-mcp
```

## CLI

| Command | Description |
|---------|-------------|
| `meetingctl doctor` | Check daemon health |
| `meetingctl start` | Start meeting (`--source none\|fixture\|mic\|mic+system`) |
| `meetingctl status` | Daemon + active meeting |
| `meetingctl note` / `mark` | Manual context |
| `meetingctl watch` | Poll transcript |
| `meetingctl stop` | Finalize + analyze |
| `meetingctl meetings` / `delete` | History |
| `meetingctl auth` | API Key / ChatGPT Subscription auth |
| `meetingctl keygen` | Encryption key |

Control API (loopback, bearer token in `~/.meetingctl/control.token`):

```text
GET  /healthz
GET  /v1/status
POST /v1/meetings
POST /v1/meetings/current/stop
GET  /v1/meetings/{id}/transcript
...
```

## Privacy

- Explicit start/stop only
- Transcripts/notes/insights encrypted at rest (AES-GCM)
- Loopback-only control plane + token auth
- Raw audio not retained by default
- Transcript text treated as untrusted in analysis prompts and MCP tools

## Development

```bash
go test ./...
go vet ./...
go build -o bin/meetingd ./cmd/meetingd
go build -o bin/meetingctl ./cmd/meetingctl
```

## Status / roadmap

**Done**

- Encrypted SQLite meeting memory
- Fixture vertical slice
- `meetingd` local API + PID lock
- CLI client → daemon
- OpenAI STT + Responses analysis adapters
- whisper.cpp + command STT adapters
- Streamable HTTP MCP on meetingd
- Per-user install scripts

**Next**

- FFmpeg mic + system capture (device adapters)
- Managed whisper.cpp download with checksums
- tunnel-client supervisor inside meetingd
- Signed release binaries + Winget
- Live capture VAD/chunk spool recovery
