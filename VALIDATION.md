# Validation status

Last local verification: 2026-07-28.

Release baseline:

```text
Go:       go1.24.13
Host:     windows/amd64
CGO:      disabled for the release build
Source:   sing-box 1.11.15 custom uTLS and Providers maintenance tree
```

## Completed checks

The current source tree has passed:

- `go test ./...`
- Focused Provider parser, manager, transaction, cache, Remote Provider, Local
  Provider, Selector, and URLTest tests.
- uTLS, REALITY, badtls, and Vision wrapper regression tests with the required
  build tags.
- `go vet ./...`
- `go mod tidy -diff` with no required `go.mod` or `go.sum` changes.
- A complete Windows amd64, `CGO_ENABLED=0` command-line build with:

```text
with_gvisor
with_quic
with_dhcp
with_wireguard
with_utls
with_reality_server
with_acme
with_clash_api
with_ech
```

- Real `sing-box check` execution for a configuration without Providers and an
  Inline Provider configuration using a Selector.
- A dependency audit confirming that source and module files do not retain
  `github.com/sagernet/utls` or `github.com/sagernet/reality`.
- Formatting and whitespace checks for the maintained Go sources.

## Covered Provider regressions

Automated or focused tests cover:

- All-or-nothing outbound transaction preparation, commit, and abort.
- Provider ownership and static-outbound collision rejection.
- Duplicate Provider tag rejection.
- Duplicate explicit Group Provider tag rejection.
- Group filter source and `*_all` scope.
- Selector persisted-selection, `default`, and first-candidate fallback order.
- Selector and URLTest rebinding to replacement outbound objects.
- Stale Provider health and URLTest result rejection after replacement.
- URLTest history cleanup for permanently removed tags.
- Group update rejection when a previously usable group would become empty.
- Remote update mutual exclusion, close waiting, and fresh download-dialer
  lookup.
- Correct update scheduling and retry backoff.
- Remote 30-second total deadline and 16 MiB response limit.
- Remote cache identity using Provider tag plus URL SHA-256.
- HTTP 304 cache-body preservation.
- HTTP 200 missing `subscription-userinfo` clearing previous traffic data.
- HTTP 304 missing `subscription-userinfo` retaining previous traffic data.
- Remote-only Tor filtering and skipped-node dependency validation.
- Local watcher close versus delayed reload synchronization.
- Provider cleanup when removed, closed before start, or abandoned during Box
  initialization.
- Parser panic recovery, strict sing-box JSON shape checks, unsupported-node
  skipping, and malformed supported-node failure.
- Node affixes, empty-name normalization, `(2)`/`(3)` duplicate naming, and
  internal detour rewriting.
- Clash VMess/VLESS SNI priority, `tls: false`, TUIC heartbeat, Hysteria port
  selection, and Hysteria2 hop-interval conversions.
- Short VMess link handling without a source panic.

## Covered uTLS regressions

The tagged checks cover:

- MetaCubeX/uTLS client and REALITY client compilation.
- MetaCubeX/uTLS REALITY server compilation.
- REALITY not implementing `WithSessionIDGenerator`.
- REALITY X25519 key-share selection through the current uTLS handshake state.
- badtls recognition of client `utls.UConn` and server `utls.Conn`.
- Replaceable wrapper-chain behavior required by VLESS Vision.
- The replaceable wrapper chain whose failure previously produced
  `not a valid supported TLS connection: *badtls.ReadWaitConn` for
  VLESS + TCP + REALITY + Vision + uTLS.

## Commands

The release build can be reproduced with:

```bash
VERSION=1.11.15-custom
CGO_ENABLED=0 go build \
  -trimpath \
  -tags 'with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech' \
  -ldflags "-X github.com/sagernet/sing-box/constant.Version=$VERSION -s -w -buildid=" \
  -o sing-box \
  ./cmd/sing-box
```

Before a release build:

```bash
go mod tidy -diff
go test ./...
go vet ./...
```

The repository also contains:

```text
.github/workflows/providers-backport-check.yml
.github/workflows/utls-backport-check.yml
```

These workflows are supplemental compatibility checks, not the recorded
Go 1.24 release baseline. They currently still specify Go 1.23.2 and should be
updated separately before they are treated as authoritative release CI.

## Validation boundary

The local Windows environment has no configured C compiler, so `go test -race`
was not run locally; the Go race detector requires CGO on this platform. This
does not affect the `CGO_ENABLED=0` release binary, but race-enabled CI remains
useful for future concurrency changes.

No local test can guarantee the behavior of every third-party subscription
server or every real network path. Before deployment, perform a short smoke
test with the intended configuration and subscription:

- Start with the actual Remote Provider URL and cache enabled.
- Confirm startup, one manual Provider update, and one HTTP 304 refresh.
- Confirm Selector manual choice persistence across restart.
- Confirm URLTest/health-check results and Provider node replacement.
- Confirm at least one real VLESS + REALITY + Vision + uTLS connection.

See `PROVIDERS-BACKPORT.md` and `UTLS-BACKPORT.md` for configuration behavior,
compatibility boundaries, and known limitations.
