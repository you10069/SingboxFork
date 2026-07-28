# sing-box 1.11.15 MetaCubeX/uTLS v1.8.4 backport

This source tree keeps the sing-box 1.11.15 architecture and configuration model,
while backporting the finalized uTLS/REALITY integration used by the 1.12 line.
It is a custom build and must not be represented as the official v1.11.15 release.

## Final dependency set

- `github.com/metacubex/utls v1.8.4`
- `github.com/sagernet/sing-vmess v0.2.7`
- `github.com/sagernet/sing-shadowtls v0.2.1-0.20250503051639-fcd445d33c11`
- `github.com/sagernet/sing v0.6.11`
- `github.com/klauspost/compress v1.17.9`
- `golang.org/x/crypto v0.33.0`
- `golang.org/x/exp v0.0.0-20240904232852-e7e105dedf7e`
- `golang.org/x/sys v0.30.0`

## Source changes

1. `common/tls/utls_client.go`
   - Replaced the archived SagerNet/uTLS import with MetaCubeX/uTLS.

2. `common/tls/reality_client.go`
   - Replaced the uTLS import.
   - Removes `X25519MLKEM768` from supported curves and key shares before the
     REALITY authentication key is calculated.
   - Uses the new `State13.KeyShareKeys.Ecdhe` handshake-state API.
   - Does **not** expose `SetSessionIDGenerator`; the REALITY session ID carries
     authentication data and must not be replaced by ShadowTLS.

3. `common/tls/reality_server.go`
   - Uses `utls.RealityConfig`, `utls.RealityServer`, and `utls.Conn`.
   - Uses the final nil-safe Reality logging callback.

4. `common/tls/reality_stub.go`
   - Keeps the 1.11 build model: the server requires both
     `with_reality_server` and `with_utls`.

5. `common/badtls/read_wait_utls.go`
   - Updates both import and `go:linkname` targets to MetaCubeX/uTLS.
   - Supports both client `utls.UConn` and server `utls.Conn` through the 1.11
     wrapper-unwrapping mechanism.

6. TLS connection wrappers
   - Marks transparent uTLS, REALITY, and badtls read/write layers as replaceable
     so Vision can safely locate the underlying TLS connection.

7. `common/tls/reality_client_utls_test.go`
   - Regression test ensuring REALITY cannot implement the generic
     `WithSessionIDGenerator` interface.

8. `go.mod`, `go.sum`, `test/go.mod`, `test/go.sum`
   - Removes the old `github.com/sagernet/utls` and
     `github.com/sagernet/reality` dependency paths.
   - Aligns Vision and ShadowTLS with MetaCubeX/uTLS-compatible releases.

## Build tags

This is still a 1.11 source tree. Do not copy the 1.12 tag list unchanged.

- REALITY client: `with_utls`
- REALITY server: `with_utls,with_reality_server`
- ECH in the 1.11 architecture: `with_ech`

Recommended complete CLI tags:

```text
with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech
```

## Recommended build

```bash
CGO_ENABLED=0 go build \
  -trimpath \
  -tags 'with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_reality_server,with_acme,with_clash_api,with_ech' \
  -ldflags '-X github.com/sagernet/sing-box/constant.Version=1.11.15-utls.1 -s -w -buildid=' \
  -o sing-box \
  ./cmd/sing-box
```

The included `.github/workflows/utls-backport-check.yml` performs module
verification, targeted package compilation, the Session ID regression test,
a complete CLI build, and an embedded dependency audit.

## Deliberately not backported

The following 1.12 features are intentionally excluded because they require
broader 1.12 architecture and API changes rather than only the uTLS migration:

- TLS fragment and TLS record fragment
- the 1.12 ECH configuration redesign
- Tailscale support
- the 1.12 DNS and domain-resolver redesign
- removal of the separate `with_reality_server` tag
