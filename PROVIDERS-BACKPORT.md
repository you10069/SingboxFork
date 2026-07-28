# Outbound Providers backport for sing-box 1.11.15

This source tree adds Outbound Providers to sing-box 1.11.15 without importing
the unrelated sing-box 1.12+ DNS, route, endpoint, and lifecycle redesigns.
The implementation follows the Provider model used by the yelnoo and reF1nd
forks, with selected stability ideas from qjebbs, but keeps the 1.11.15
architecture and configuration boundary.

This is a custom maintenance build, not an official sing-box v1.11.15 release.

## Supported Provider types

The top-level `providers` array accepts:

- `inline`: nodes embedded in the main configuration.
- `local`: nodes read from a local file and automatically reloaded after file
  changes.
- `remote`: nodes downloaded over HTTP or HTTPS, with optional persistent
  cache, ETag refresh, filtering, and automatic updates.

Every Provider accepts `type`, `tag`, `additional_prefix`,
`additional_suffix`, and `health_check`.

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `type` | Yes | — | `inline`, `local`, or `remote`. |
| `tag` | No | Provider array index | Provider identifier. Use an explicit stable tag in production. Duplicate Provider tags are rejected. |
| `additional_prefix` | No | Empty | Text prepended verbatim to every subscription node name. |
| `additional_suffix` | No | Empty | Text appended verbatim to every subscription node name. |
| `health_check` | No | Disabled | Independent automatic health-check settings. |

### Inline Provider

| Field | Required | Meaning |
| --- | --- | --- |
| `outbounds` | Yes in practice | Embedded sing-box outbound objects. At least one supported valid outbound must remain. |

```json
{
  "providers": [
    {
      "type": "inline",
      "tag": "embedded",
      "outbounds": [
        {
          "type": "socks",
          "tag": "node-a",
          "server": "127.0.0.1",
          "server_port": 1080
        }
      ]
    }
  ],
  "outbounds": [
    {
      "type": "selector",
      "tag": "select",
      "providers": ["embedded"],
      "default": "embedded_node-a"
    }
  ],
  "route": {
    "final": "select"
  }
}
```

### Local Provider

| Field | Required | Meaning |
| --- | --- | --- |
| `path` | Yes | Subscription file path. Relative paths follow sing-box's configured base path. |

```json
{
  "providers": [
    {
      "type": "local",
      "tag": "local-sub",
      "path": "providers/local.yaml",
      "additional_suffix": " [local]"
    }
  ],
  "outbounds": [
    {
      "type": "urltest",
      "tag": "auto",
      "providers": ["local-sub"],
      "url": "https://www.gstatic.com/generate_204",
      "interval": "5m"
    }
  ],
  "route": {
    "final": "auto"
  }
}
```

The initial file load is strict. A missing or invalid file prevents startup.
Later file changes are parsed and published transactionally; a failed reload
leaves the previous nodes active.

### Remote Provider

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `url` | Yes | — | HTTP or HTTPS subscription URL. |
| `user_agent` | No | `sing-box <version>` | HTTP `User-Agent`. |
| `download_detour` | No | Current default outbound | Outbound used for every download connection. |
| `update_interval` | No | `24h` | Successful-update interval; values below `1m` are raised to `1m`. |
| `disable_auto_update` | No | `false` | Disables startup refresh of an existing cache and the background download loop. |
| `include` | No | Empty | Regular expression; retain only matching node names. |
| `exclude` | No | Empty | Regular expression; matching node names are removed. `exclude` takes precedence over `include`. |

```json
{
  "experimental": {
    "cache_file": {
      "enabled": true
    }
  },
  "providers": [
    {
      "type": "remote",
      "tag": "subscription",
      "url": "https://example.com/subscription",
      "user_agent": "sing-box",
      "download_detour": "direct",
      "update_interval": "24h",
      "disable_auto_update": false,
      "include": "Hong Kong|Japan",
      "exclude": "Expire|Traffic",
      "health_check": {
        "enabled": true,
        "url": "https://www.gstatic.com/generate_204",
        "interval": "10m",
        "timeout": "5s"
      }
    }
  ],
  "outbounds": [
    {
      "type": "selector",
      "tag": "select",
      "providers": ["subscription"]
    }
  ],
  "route": {
    "final": "select"
  }
}
```

Set `download_detour` explicitly when `route.final` points to a group populated
only by the same Remote Provider. `download_detour: "direct"` avoids a circular
first-download dependency.

The download dialer is resolved for every new HTTP connection. Replacing the
default outbound or the configured detour therefore does not leave the Remote
Provider bound to an obsolete Go object.

## Node naming

Provider node names are normalized before they are registered as dynamic
outbounds.

Without `additional_prefix`, the runtime name is:

```text
<Provider tag>_<normalized subscription node name>
```

For example, Provider `airport` and node `Hong Kong` become:

```text
airport_Hong Kong
```

When `additional_prefix` is non-empty, the automatic `<Provider tag>_` prefix
is omitted. The runtime name is exactly:

```text
<additional_prefix><subscription node name><additional_suffix>
```

The two user-provided affixes are concatenated verbatim. No `_`, `-`, or space
is inserted automatically, so the user controls all separators.

| Configuration | Input node | Runtime name |
| --- | --- | --- |
| No affixes, Provider `airport` | `Hong Kong` | `airport_Hong Kong` |
| `additional_suffix: " [A]"` only | `Hong Kong` | `airport_Hong Kong [A]` |
| `additional_prefix: "[A] "` | `Hong Kong` | `[A] Hong Kong` |
| Prefix `[A] ` and suffix ` - VIP` | `Hong Kong` | `[A] Hong Kong - VIP` |

Additional naming rules:

- Empty node names use their zero-based subscription index.
- The first occurrence keeps its name.
- Duplicates become `name (2)`, `name (3)`, and so on.
- Existing names such as `name (2)` are respected when choosing the next free
  suffix.
- Internal node `detour` references are rewritten to the normalized runtime
  names.
- A Provider can replace only nodes it already owns. A new node may not
  overwrite a static outbound or another Provider's node.

Because `additional_prefix` removes the automatic Provider namespace, choose
it carefully. Any resulting collision with an existing outbound rejects the
Provider startup or update instead of replacing that outbound.

## Subscription formats and parser behavior

Supported inputs are:

- A sing-box JSON document containing an `outbounds` array.
- Clash/Mihomo YAML containing a `proxies` array.
- SIP008 Shadowsocks JSON.
- Raw or Base64-encoded link lists using `ss`, `vmess`, `trojan`, `vless`,
  `tuic`, `hysteria`, `hy2`, or `hysteria2`.

Parsing is intentionally tolerant of unsupported nodes but strict about nodes
that are expected to work:

- A panic inside one parser is recovered and reported as a parser error; it
  cannot crash sing-box.
- Sing-box JSON requires `outbounds` to be an array, every outbound to be an
  object, and every `type` to be a non-empty string.
- `direct`, `block`, `dns`, `selector`, and `urltest` are not valid Provider
  nodes and are skipped.
- Outbound types unavailable in this 1.11.15 build are skipped with a warning.
- Remote Providers additionally skip `tor`. Local, Inline, and ordinary static
  `tor` outbounds are not affected.
- A supported retained node with malformed options rejects the complete
  startup or update.
- A retained node whose `detour` points to a skipped node rejects the complete
  startup or update.
- A Provider with no supported retained node is rejected.

Consequently, a subscription containing one unsupported node and several valid
nodes loads the valid nodes. A subscription containing one malformed supported
node does not publish a partial result.

The Clash/Mihomo parser also contains compatibility fixes for:

- Explicit VMess/VLESS `sni` taking priority over fallback server-name fields.
- `tls: false` remaining disabled.
- TUIC heartbeat duration units.
- Hysteria2 `hop-interval` duration units.
- Hysteria port ranges selecting the first port.
- Short or malformed VMess links returning an error instead of panicking.
- Base64 decoding failures returning the real decoding error.

## Remote download and cache behavior

Remote downloads use the following fixed safety boundaries:

- A 30-second total request deadline, including up to three attempts and the
  one-second gaps between attempts.
- A 16 MiB HTTP response-body limit, enforced both from
  `Content-Length` and while streaming.
- HTTP redirects remain enabled for subscription services that redirect to a
  CDN, signed URL, or generated endpoint.
- URL-bearing `url.Error` values replace the subscription URL with
  `<provider-url>` before logging or returning the error.

Automatic update timing is based on the last successful update:

- Default interval: 24 hours.
- Minimum interval: 1 minute.
- Failed automatic updates retry with exponential delays starting at 1 minute.
- Retry delay is capped at 30 minutes, or at `update_interval` when that is
  lower.
- A successful update schedules the next run from its actual completion time;
  the interval is not accidentally doubled.

Persistent Provider cache is used only when
`experimental.cache_file.enabled` is true. Its identity is:

```text
<Provider tag>#SHA-256(<subscription URL>)
```

Changing the URL therefore cannot restore the previous URL's cache. Old
tag-only Provider cache entries are intentionally not treated as compatible.
The cached payload is the decoded original subscription body, not the runtime
namespaced options, so restarting cannot add the Provider prefix twice.

Startup behavior:

- With no usable cache, Remote Provider download and construction are
  synchronous. Failure prevents startup.
- A matching cache is usable only if it can be parsed, filtered, validated,
  constructed, and published completely.
- A broken matching cache is ignored and the current URL is downloaded.
- With a usable fresh cache and automatic updates enabled, the cache is used
  until its interval becomes due.
- With a usable stale cache, a refresh failure keeps the cached nodes and starts
  the retry loop.
- With `disable_auto_update: true`, a usable cache is restored without a
  startup refresh and no background download loop is created. If no usable
  cache exists, one initial download is still required.
- `disable_auto_update` does not disable a manual Remote Provider update
  through the Clash API.

HTTP metadata behavior:

- `ETag` is sent as `If-None-Match` on later downloads.
- HTTP 304 retains the active subscription body and, when the response omits
  `subscription-userinfo`, retains the previous traffic information.
- HTTP 200 without `subscription-userinfo` clears old traffic information.
- HTTP 304 without an active node set, or without the required cached body,
  fails instead of pretending the Provider is valid.
- Subscription info found on the first decoded subscription line is removed only when it is
  actually recognized as subscription metadata; ordinary first-line content is
  preserved.

The cache write is part of the update commit. A cache write failure rejects the
new online result and leaves the old Provider active. This keeps cache, ETag,
groups, and active nodes strictly consistent, at the cost of making a cache
storage failure block that update.

## Health checks

Every Provider supports:

```json
{
  "health_check": {
    "enabled": true,
    "url": "https://www.gstatic.com/generate_204",
    "interval": "10m",
    "timeout": "3s"
  }
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Enables periodic automatic checks. |
| `url` | Empty | Test URL. Set it when automatic checks are enabled. |
| `interval` | `10m` | Check interval; values below `1m` are raised to `1m`. |
| `timeout` | `3s` | Per-node check timeout. |

Subscription updates and health checks are independent:

- `disable_auto_update` controls Remote subscription downloads only.
- `health_check.enabled` controls periodic health checks only.
- A manual Clash API health check remains available when automatic checks are
  disabled.
- Provider replacement cancels stale in-progress results and, when automatic
  health checks are enabled, schedules a check for the new objects.
- Provider close cancels the health context, stops its ticker, waits for the
  worker, and then removes the nodes. No health-check goroutine is intentionally
  left behind.

## Selector and URLTest integration

Selector and URLTest accept these common fields:

```json
{
  "outbounds": ["direct", "manual-node"],
  "providers": ["subscription-a"],

  "include_all": false,
  "include_all_outbounds": false,
  "use_all_providers": false,

  "include": "(?i)(HK|JP)",
  "exclude": "(?i)(expire|traffic)",
  "exclude_all": false,

  "exclude_type": "(?i)(trojan|tuic)",
  "exclude_type_all": false
}
```

`include_all` enables both `include_all_outbounds` and `use_all_providers`.
Automatic static collection uses ordinary static outbounds declared in the
main configuration. Selector, URLTest, and DNS outbounds are not collected
automatically, but they can still be listed explicitly in `outbounds` where
normal dependency validation permits it.

| Candidate source | `include` | `exclude` by default | `exclude_type` by default |
| --- | --- | --- | --- |
| Explicit `outbounds` | Bypassed | Bypassed | Bypassed |
| Automatically collected static outbounds | Applied | Applied | Applied |
| Nodes from explicit or all Providers | Applied | Applied | Applied |

`exclude_all: true` also applies `exclude` to explicit `outbounds`.
`exclude_type_all: true` also applies `exclude_type` to explicit `outbounds`.
Explicit candidates win source classification when the same tag is collected
again automatically. Final membership is de-duplicated by outbound tag.

When `use_all_providers` or `include_all` is false, duplicate entries in the
explicit `providers` array are rejected. When all Providers are requested, the
explicit array is ignored and the Provider manager's unique list is used.

Selector-specific fields:

- `default`: startup fallback after any persisted manual selection.
- `interrupt_exist_connections`: interrupts connections after the selected
  outbound changes, including changes caused by Provider replacement.

Initial Selector choice is resolved in this order after all Providers finish
their first load:

1. A persisted manual selection, if it is still present.
2. `default`, if configured and present.
3. The first available candidate.

If a previously selected node is absent after restart or a Provider update, the
same fallback order selects `default` and then the first available candidate.
A missing configured `default` is a startup error after Provider initialization
is complete.

URLTest-specific fields remain `url`, `interval`, `tolerance`, `idle_timeout`,
and `interrupt_exist_connections`. Provider replacement invalidates old object
results, cancels or queues overlapping tests, and prevents a delayed result for
an old object from overwriting the new state.

## Transaction and lifecycle guarantees

Provider node replacement is a complete transaction:

1. Parse, filter, validate, dependency-sort, construct, and start all candidate
   nodes in an isolated staging view.
2. Prepare every dependent Selector and URLTest group against that complete
   candidate set.
3. For Remote Providers with cache enabled, save the candidate cache state.
4. Publish the new nodes and prepared group state together under the outbound
   manager transaction.
5. Close replaced objects and remove URLTest history for permanently removed
   tags.

Any failure aborts and closes the staged nodes. The previous Provider nodes,
groups, cache, ETag, subscription information, and successful update time
remain active. Partial old/new node sets are not committed.

A runtime update that would turn a previously usable dependent Selector or
URLTest group into an empty group is rejected, preserving the old subscription
and group. During startup a group may be temporarily empty while Providers are
loading, but startup rejects it after all Providers finish if it still has no
candidate.

Static outbounds are created before Providers. Provider node ownership rules
then prevent a Provider from replacing static outbounds or another Provider's
nodes. Provider initialization failure, removal before start, normal close, and
Box initialization failure all close already-created Provider resources.
Remote Provider close cancels new work and waits for any in-flight update
transaction before deleting nodes.

## Clash API

With `with_clash_api`, the following Mihomo-compatible paths are available:

```text
GET  /providers/proxies
GET  /providers/proxies/{name}
PUT  /providers/proxies/{name}
GET  /providers/proxies/{name}/healthcheck
```

Behavior:

- GET endpoints return Provider type, name, update time, node list, and Remote
  subscription information when available.
- PUT performs an immediate network update only for a Remote Provider.
- PUT on Local or Inline currently returns HTTP 204 without reloading or
  changing the Provider.
- The health-check endpoint manually tests the current nodes regardless of
  `health_check.enabled`.

## Behavior without Providers

Existing 1.11.15 configurations do not need a `providers` field. When it is
absent or empty, no Provider is created and no Provider download, file watcher,
automatic update loop, or Provider health-check loop runs. Provider build tags
are not required, and ordinary static outbound, route, DNS, and inbound
configuration remains available as before.

## Compatibility boundary and known limitations

This backport intentionally does not add:

- Endpoint Providers.
- Provider `override_dialer`.
- AnyTLS, Tailscale, Snell, or kTLS.
- Newer `loadbalance`, `pass`, or other post-1.11 group architectures.
- sing-box 1.12+ DNS, route, endpoint, TLS-fragmentation, or lifecycle APIs.
- `dedup_host` or `dedup_host_port`; nodes sharing an address can legitimately
  differ by UUID, password, SNI, REALITY key, or transport settings.

Current deliberate limitations and trade-offs:

- An HTTP 200 response rebuilds the Provider nodes even when its body is
  byte-for-byte identical. HTTP 304 is the no-rebuild path.
- URLTest and Provider health histories use node tags as keys rather than the
  Provider URL identity.
- Remote Providers forbid `tor`, but this is not a complete security allowlist
  for every possible registered outbound type.
- Local reload is file-watcher driven and Inline is immutable after creation;
  their Clash API PUT operation is a no-op.
- Provider cache methods are attached to the general internal `CacheFile`
  interface. Third-party Go implementations or mocks of that interface must
  implement the added methods even when they do not use Providers.
- `SelectorOutboundOptions` and `URLTestOutboundOptions` embed
  `GroupCommonOptions`. External Go code using structure literals must
  initialize that embedded field explicitly; JSON configuration is unaffected.
- Cache I/O occurs inside the strict publication transaction. Slow cache
  storage can briefly delay outbound manager access, and cache failure rejects
  an otherwise usable online update.

## Build

Providers add no build tag. A complete CGO-free build with the maintained uTLS,
REALITY server, Clash API, and ECH features can use:

```bash
VERSION=1.11.15-custom
CGO_ENABLED=0 go build \
  -trimpath \
  -tags 'with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech' \
  -ldflags "-X github.com/sagernet/sing-box/constant.Version=$VERSION -s -w -buildid=" \
  -o sing-box \
  ./cmd/sing-box
```

Use Go 1.24 for the maintained release build. See `UTLS-BACKPORT.md` for the
uTLS/REALITY compatibility changes and `VALIDATION.md` for the latest verified
commands and results.
