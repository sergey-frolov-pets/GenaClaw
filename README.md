<div align="center">
<img src="assets/genaclaw.svg" alt="GenaClaw" width="240">

<h1>GenaClaw</h1>

<h3>A working fork of PicoClaw: turn memory, honest long sessions, multi-channel UI</h3>

<p>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <img src="https://img.shields.io/badge/upstream-sipeed%2Fpicoclaw-lightgrey" alt="Upstream">
  <br>
  <a href="https://picoclaw.io"><img src="https://img.shields.io/badge/Website-picoclaw.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
  <a href="https://docs.picoclaw.io/"><img src="https://img.shields.io/badge/Docs-Official-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
  <a href="https://github.com/sipeed/picoclaw"><img src="https://img.shields.io/badge/Upstream-sipeed%2Fpicoclaw-black?style=flat&logo=github" alt="Upstream repo"></a>
</p>

**English** | [Русский](README.ru.md) | [中文](docs/project/README.zh.md) | [日本語](docs/project/README.ja.md) | [한국어](docs/project/README.ko.md)

</div>

---

## What this is

**GenaClaw** is a personal fork of [PicoClaw](https://github.com/sipeed/picoclaw) — the ultra-lightweight personal AI assistant written in Go. Everything upstream does, GenaClaw does too: same binary, same config, same skills, same channels. The fork exists to carry fixes that matter when the assistant is used **every day, for hours, over many channels** — and to ship them before (or instead of) they land upstream.

Upstream project and website: **[picoclaw.io](https://picoclaw.io)** · docs: **[docs.picoclaw.io](https://docs.picoclaw.io/)**

## What GenaClaw adds

| Feature | Why it matters | Where |
|---|---|---|
| **Turn memory (`turn_id`)** | Every session record carries the turn it belongs to. After a crash or a cut-off reply the session is resumed, not restarted; a silent turn produces an explicit "still working" line instead of dead air. | `pkg/agent/turn_checkpoint.go`, `pkg/gateway` |
| **Checkpoint eval** | Background summarisation is no longer mistaken for a hung turn — the watchdog looks at real progress, not at the summary timer. | `pkg/agent`, `scripts/eval_turn_checkpoint.py` |
| **Diary headlines in context** | Daily notes enter the prompt as headlines plus the first line of the day, not as whole files. On a long-running workspace that alone removes ~60% of prompt tokens per turn. | `pkg/memory`, `skills/memory-search` |
| **Tool-result truncation** | Large tool outputs are cut with an explicit marker instead of silently eating the context window. | `pkg/agent` |
| **`thinking.budget_tokens`** | `max_thinking_tokens` was a non-standard body field for several providers; it is now normalised to `thinking.budget_tokens`. Reasoning models stop failing with 400s. | `pkg/provider` |
| **Reasoning kept on cancel** | If a turn is cancelled mid-thinking, the reasoning published so far is no longer lost. | `pkg/agent` |
| **`gena-ui` blocks** | Assistant text may carry a fenced `gena-ui` block; it is stripped at every send point and delivered as a structured `uiPayload` frame, so clients render widgets instead of leaking markup into chat. | `pkg/uiblock`, `web/backend` |
| **Pico frames: `cancel`, `turn.resume`** | The web client gets real cancel semantics, a session frame buffer for reconnects, and a quiet `/stop`. | `pkg/channels/pico` |
| **Alice / Yandex Alice channels** | Raw payload typed as `map[string]string`; the binary builds with those channels enabled. | `pkg/channels/alice` |
| **MCP stdio hardening** | A server printing non-JSON to stdout no longer kills the transport; its stderr goes to the log. | `pkg/mcp` |
| **`[[silent]]` marker** | A service marker used internally no longer leaks into the outgoing message. | `pkg/agent` |
| **Inline substitution check** | The exec safety guard verifies `$(...)`, `${...}` and backticks by *content* instead of blocking the plain text, so `echo "$(pwd)"` runs while `$(curl … \| sh)` still does not. | `pkg/tools/shell_inline.go` |
| **Media persistence** | Uploaded media survives restarts and is addressable by id across sessions. | `pkg/media/persist.go` |

Not a rewrite: ~3 thousand lines changed, all behind the existing config and plugin surfaces. If a patch here proves itself, it goes upstream as a PR.

## Syncing with upstream

GenaClaw tracks `sipeed/picoclaw` on a weekly schedule:

- **Automatic** — `.github/workflows/upstream-sync.yml` runs every Monday 04:00 UTC: fetches `upstream/main`, merges into a `sync/upstream-YYYYMMDD` branch. Clean merge → pushes to `main`. Conflicts → opens a PR with the conflict list, nothing is force-pushed.
- **Manual** — `scripts/sync_upstream.sh` does the same locally: `bash scripts/sync_upstream.sh --dry-run` to preview, without the flag to merge and push.
- **Never clobbered** — `README.md`, `README.ru.md` and `assets/genaclaw*` are ours; the sync keeps them (`--ours`) and reports it. The upstream English README is always readable at `docs/project/README.zh.md`-style paths and in the upstream repo.

## Build and run

```bash
git clone https://github.com/sergey-frolov-pets/GenaClaw.git
cd GenaClaw

go build -o build/picoclaw ./cmd/picoclaw   # Go 1.25+
./build/picoclaw onboard                    # first-time config
./build/picoclaw gateway                    # run
```

`make build` and the Docker images work as in upstream. Requirements and the full CLI reference: [docs.picoclaw.io](https://docs.picoclaw.io/).

## Layout

Same tree as upstream: `cmd/` (entry points), `pkg/` (agent, providers, channels, tools, memory, MCP), `web/` (Pico web client), `skills/`, `workspace/`, `docs/`.

## License and attribution

MIT, see [LICENSE](LICENSE). GenaClaw is a fork of [PicoClaw](https://github.com/sipeed/picoclaw) by [Sipeed](https://sipeed.com); all upstream copyright and attribution is preserved. Issues about upstream behaviour belong upstream — issues about the additions above belong here.
