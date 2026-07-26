# Outbound Providers backport for sing-box 1.11.15

This source tree is based on the existing custom sing-box 1.11.15 uTLS maintenance build and adds **Outbound Providers** without importing the unrelated sing-box 1.12+ feature set.

## Scope

Included provider types:

- `inline`: nodes embedded in the main configuration.
- `local`: nodes loaded from a local file and reloaded when the file changes.
- `remote`: nodes downloaded over HTTP/HTTPS, with ETag and cache-file support.

Supported subscription inputs:

- sing-box outbound JSON documents.
- Clash/Mihomo proxy YAML.
- SIP008 Shadowsocks JSON.
- Raw or Base64-encoded subscription links for the protocols implemented by sing-box 1.11.15.

Provider nodes are registered as:

```text
<provider tag>/<node tag>
```

Selector and URLTest outbounds can combine explicit static outbounds,
automatically collected static outbounds and Provider nodes:

```json
{
  "type": "selector",
  "tag": "select",
  "outbounds": ["direct", "manual-node"],
  "providers": ["subscription"],

  "include_all": false,
  "include_all_outbounds": true,
  "use_all_providers": true,

  "include": "Hong Kong|Japan",
  "exclude": "Expire|Traffic",
  "exclude_all": false,
  "exclude_type": "tuic|hysteria2",
  "exclude_type_all": false
}
```

`include_all` enables both `include_all_outbounds` and
`use_all_providers`. `include` applies to automatically collected static
outbounds and Provider nodes. Explicit `outbounds` bypass `include`; they also
bypass `exclude` and `exclude_type` unless the corresponding `*_all` option is
set. Selector and URLTest outbounds are never automatically collected as
static candidates.

## Remote provider example

```json
{
  "providers": [
    {
      "type": "remote",
      "tag": "subscription",
      "url": "https://example.com/subscription",
      "user_agent": "sing-box",
      "download_detour": "direct",
      "update_interval": "24h",
      "disable_auto_update": false,
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

Set `download_detour` explicitly when `route.final` points to a selector that is populated only by the same remote provider. Using `direct` avoids a circular first-download dependency.

### Disable automatic subscription updates

Set:

```json
"disable_auto_update": true
```

to disable startup refresh of an existing cache and to avoid creating the background subscription-update ticker. The behavior is:

- With no usable cache, the provider still downloads once during initial startup.
- With a usable cache, startup restores the cache without checking its age or contacting the subscription URL.
- Manual updates through the Clash API remain available.
- Provider health checks are independent and continue when `health_check.enabled` is `true`.
- Omitting the field, or setting it to `false`, preserves the existing automatic-update behavior.

## Local provider example

```json
{
  "providers": [
    {
      "type": "local",
      "tag": "local-sub",
      "path": "providers/local.yaml"
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
  ]
}
```

## Inline provider example

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
      "default": "embedded/node-a"
    }
  ]
}
```

## Compatibility boundary

This backport intentionally does not add:

- Endpoint Providers.
- AnyTLS.
- Tailscale.
- Snell.
- kTLS.
- sing-box 1.12 DNS, route, endpoint, or TLS-fragmentation changes.

Provider subscription parsers skip non-proxy outbounds such as `direct`, `block`, `dns`, `selector`, and `urltest`. Outbound types not registered in the current 1.11.15 build, such as AnyTLS, are also skipped with a warning. A retained supported outbound with invalid options rejects the complete Provider load or update.

## Build tags

Providers do not require a new build tag. Continue using the existing 1.11.15 maintenance-build tags. For a complete CLI build with REALITY server support:

```bash
CGO_ENABLED=0 go build \
  -trimpath \
  -tags 'with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech' \
  -ldflags '-X github.com/sagernet/sing-box/constant.Version=1.11.15-utls-providers.5 -s -w -buildid=' \
  -o sing-box \
  ./cmd/sing-box
```

## Clash API

When `with_clash_api` is enabled, the following paths expose and control proxy providers:

```text
GET  /providers/proxies
GET  /providers/proxies/{name}
PUT  /providers/proxies/{name}
GET  /providers/proxies/{name}/healthcheck
```

## Notes

- Remote subscription cache stores the decoded original subscription body, not runtime-prefixed outbound options. This prevents duplicate provider prefixes after restart.
- Internal detours between nodes in the same provider are rewritten to the namespaced tags at runtime.
- Empty or duplicate node tags are normalized before dynamic outbounds are created.
- Provider startup is strict: without a complete usable cache, download, parse or retained-node creation failure prevents `Box.Start()` from succeeding.
- Runtime Provider updates are transactional. All retained nodes are staged and started before publication; any failure leaves the previous Provider, Group, cache, ETag and update time unchanged.
- Selector and URLTest state is prepared against the complete candidate Provider set and published in the same transaction before replaced objects are closed.
- URLTest history for permanently removed Provider tags is deleted after a successful commit.
