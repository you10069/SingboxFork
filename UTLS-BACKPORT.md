# MetaCubeX/uTLS backport for sing-box 1.11.15

This source tree keeps the sing-box 1.11.15 configuration and lifecycle model
while backporting the compatible uTLS and REALITY integration used by later
sing-box releases. Official 1.12.25 and 1.13.15 source trees were used only to
identify the finalized fixes that can be safely applied to 1.11.15.

This is a custom maintenance build, not an official sing-box v1.11.15 release.

## Dependency set

The maintained dependency combination is:

- `github.com/metacubex/utls v1.8.4`
- `github.com/sagernet/sing v0.6.11`
- `github.com/sagernet/sing-vmess v0.2.7`
- `github.com/sagernet/sing-shadowtls v0.2.1-0.20250503051639-fcd445d33c11`
- `github.com/klauspost/compress v1.17.9`
- `golang.org/x/crypto v0.33.0`
- `golang.org/x/exp v0.0.0-20240904232852-e7e105dedf7e`
- `golang.org/x/sys v0.30.0`

The archived `github.com/sagernet/utls` and
`github.com/sagernet/reality` module paths are not retained.

## uTLS and REALITY changes

### uTLS client

`common/tls/utls_client.go`:

- Uses `github.com/metacubex/utls`.
- Keeps the 1.11.15 TLS configuration interface.
- Exposes the underlying `utls.UConn` through the wrapper chain.
- Marks the wrapper reader and writer as replaceable so protocol layers such as
  VLESS Vision can locate and replace the correct transport layer.

### REALITY client

`common/tls/reality_client.go`:

- Uses the MetaCubeX/uTLS handshake state.
- Removes `X25519MLKEM768` from advertised curves and key shares before
  rebuilding the REALITY ClientHello. REALITY authentication in this backport
  uses the classical X25519 key share.
- Reads the ephemeral key from `State13.KeyShareKeys.Ecdhe`.
- Keeps REALITY's authenticated session ID under REALITY control.
- Does not implement the generic `WithSessionIDGenerator` interface, preventing
  ShadowTLS from replacing authentication data in the ClientHello session ID.
- Exposes the underlying `utls.UConn` and marks its wrapper reader and writer as
  replaceable.

`common/tls/reality_client_utls_test.go` contains the regression check that
REALITY cannot be treated as `WithSessionIDGenerator`.

### REALITY server

`common/tls/reality_server.go`:

- Uses `utls.RealityConfig`, `utls.RealityServer`, and `utls.Conn`.
- Uses the nil-safe REALITY logging callback.
- Exposes the underlying server-side `utls.Conn`.
- Marks the wrapper reader and writer as replaceable.

The 1.11.15 build boundary remains intact:

- REALITY client requires `with_utls`.
- REALITY server requires both `with_utls` and `with_reality_server`.

## VLESS Vision, REALITY, and badtls fix

The complete wrapper-chain fix is included for:

```text
VLESS + TCP + REALITY + Vision + uTLS
```

Before the fix, Vision could receive a `*badtls.ReadWaitConn` that it could not
unwrap to a recognized TLS connection and fail with:

```text
not a valid supported TLS connection: *badtls.ReadWaitConn
```

The problem was not the VLESS or REALITY configuration. The uTLS migration had
changed the concrete TLS objects while the 1.11 badtls/Vision discovery path
still depended on a transparent replaceable-wrapper chain.

The full correction is:

- `utlsConnWrapper`, REALITY client wrapper, and REALITY server wrapper expose
  their upstream connection and are reader/writer replaceable.
- `badtls.ReadWaitConn` exposes its wrapped TLS connection and is reader
  replaceable.
- `common/badtls/read_wait_utls.go` recognizes both client `utls.UConn` and
  server `utls.Conn`.
- Its `go:linkname` targets point to the MetaCubeX/uTLS `Conn` methods.

This allows Vision to traverse the wrapper chain to the actual supported uTLS
connection instead of stopping at `ReadWaitConn`. The change is limited to
connection-wrapper discoverability; it does not alter ordinary VLESS routing,
flow selection, or non-Vision connections.

## Build tags

This remains a 1.11.15 source tree. Do not copy the complete 1.12 or 1.13 tag
model into it.

- uTLS and REALITY client: `with_utls`
- REALITY server: `with_utls,with_reality_server`
- ECH in the 1.11 architecture: `with_ech`
- Set `without_badtls` only if the badtls read-wait optimization is
  intentionally disabled.

Recommended complete tags:

```text
with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech
```

Recommended CGO-free build:

```bash
VERSION=1.11.15-custom
CGO_ENABLED=0 go build \
  -trimpath \
  -tags 'with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech' \
  -ldflags "-X github.com/sagernet/sing-box/constant.Version=$VERSION -s -w -buildid=" \
  -o sing-box \
  ./cmd/sing-box
```

Use Go 1.24 for the maintained release build.

## Deliberately not backported

The following newer features require broader architecture and API changes and
are intentionally excluded:

- TLS fragment and TLS record fragment.
- The 1.12 ECH configuration redesign.
- Removal of the separate `with_reality_server` tag.
- Tailscale support.
- The 1.12+ DNS and domain-resolver redesign.
- Endpoint and other post-1.11 lifecycle changes.

See `VALIDATION.md` for the current local verification results.
