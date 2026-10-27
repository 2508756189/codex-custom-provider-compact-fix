# codex-local-compact

Native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (cli-proxy-api) plugin, version **0.1.0**.

Plugin id and name: **`codex-local-compact`**. That id is the dynamic-library filename without its extension. CLIProxyAPI also accepts a version suffix and strips a trailing `-v<version>` when the remainder is a valid plugin id, so `codex-local-compact-v0.1.0.dll` still registers as `codex-local-compact`.

## Purpose

Some providers do not return Codex remote compaction (`type=compaction`). This plugin:

- Routes Codex / OpenAI Responses compaction requests for allowlisted models to itself.
- Asks the host to run a local, non-streaming summary (`host.model.execute`), seals that summary in a `clc-v1` capsule, and returns a Codex-compatible compaction item (`type=compaction` with `encrypted_content`).
- On later turns, expands its own capsules back into a user context message and drops foreign compaction blobs the model cannot read.
- Ensures DeepSeek thinking mode gets `reasoning_text` on replayed assistant turns. Existing reasoning text is kept. Empty reasoning items are filled from their summary text or from `reasoning_placeholder`. Turns that have no reasoning item get one. Follow-up requests after compaction then satisfy DeepSeek instead of failing with HTTP 400.

The plugin does not call tools during the summary hop, and it does not forward `compaction_trigger` upstream.

## Target

CLIProxyAPI native plugin. The intended build is a **Windows amd64 DLL** produced with CGO and `-buildmode=c-shared`. The export is `cliproxy_plugin_init`.

Declared capabilities: model router, executor (`openai-response`, `openai`, `codex`), request normalizer, and request interceptor.

## Install

Keep the DLL in an **external** `plugins.dir`, outside the CLIProxyAPI install tree, so replacing or overwriting the CPA install does not delete it.

Example layout:

```text
<plugins.dir>/windows/amd64/codex-local-compact-v0.1.0.dll
```

`plugins.dir` is searched as `plugins/<GOOS>/<GOARCH>` and then the directory root. On Windows the library extension is `.dll`.

In CPA `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: "C:/cpa-plugins" # external directory, not inside the CPA install
  configs:
    codex-local-compact:
      enabled: true
      priority: 50
      match_substrings:
        - deepseek
```

`match_substrings` is matched case-insensitively against the model name. The default, when both `models` and `match_substrings` are empty, is `[deepseek]`.

To allowlist exact model names instead, set `models` (case-insensitive). When `models` is non-empty, only those names match.

```yaml
plugins:
  configs:
    codex-local-compact:
      enabled: true
      priority: 50
      models:
        - deepseek-chat
```

Other plugin fields (all optional):

| Field | Role |
| --- | --- |
| `enabled` | When false, the plugin declines compaction routing and request rewrites. Default `true`. |
| `priority` | Host ordering. Higher runs first. Default `50` in this plugin. |
| `reasoning_placeholder` | Used when `reasoning_text` was not retained. Default `[reasoning not retained for this turn]`. |
| `summary_prompt` | Override for the local-compact summary prompt. |
| `debug_log_file` | If set, append metadata-only lines (no request bodies). |

## DeepSeek via CLIProxyAPI

For official DeepSeek through CPA, the model or key entry also needs `is-compat: true`. Otherwise CPA's own sanitizer rewrites `reasoning_text` and DeepSeek still rejects the follow-up.

```yaml
models:
  - name: "deepseek-chat" # your configured DeepSeek model name
    is-compat: true
```

`is-compat` belongs on the CPA model entry, not only under `plugins.configs`.

## Build (Windows amd64)

`go.mod` requires **Go 1.26.10** and replaces the CLIProxyAPI module with a local checkout:

```text
replace github.com/router-for-me/CLIProxyAPI/v8 => /workspace/cpa-src/CLIProxyAPI-main
```

Point that `replace` at your own CLIProxyAPI source tree before testing or building. The `require` version is `v8.0.0` (`github.com/router-for-me/CLIProxyAPI/v8`). The plugin imports `sdk/pluginabi` and `sdk/pluginapi` from that tree.

This module uses cgo (`import "C"` in `main.go`). CLIProxyAPI's plugin examples build Go plugins with:

```text
go build -buildmode=c-shared -o <library> .
```

On Windows amd64, from this directory, with CGO enabled and a C compiler for that target:

```powershell
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -buildmode=c-shared -o codex-local-compact-v0.1.0.dll .
```

`go build -buildmode=c-shared` also writes a matching `.h` file next to the DLL. CPA loads the DLL only. Do not commit the DLL or the generated header.

There is no Makefile and no extra `-ldflags` in this tree. Cross-compiling from another OS needs a Windows C toolchain already available to CGO; this repository does not set `CC`.

Check the result with:

```text
go test ./...
```

## Source

| File | Role |
| --- | --- |
| `main.go` | C ABI exports, registration, host callbacks |
| `config.go` | Plugin YAML (`plugins.configs.codex-local-compact`) |
| `compact.go` | Local summary and Codex compaction response |
| `capsule.go` | `clc-v1` seal and expand |
| `reasoning.go` | `reasoning_text` passback and capsule replay |
| `normalize.go` | Request normalizer and interceptor |
| `host.go` | Host model execution and stream emit/close |
| `logic_test.go` | Passback, capsule, allowlist, and summary-body tests |
| `go.mod`, `go.sum` | Module definition |

Version in the registration metadata is `0.1.0`. This repository does not publish older releases.
