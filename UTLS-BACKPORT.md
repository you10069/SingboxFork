# sing-box 1.11.15 → MetaCubeX/uTLS v1.8.4 回移说明

本补丁只围绕 uTLS/REALITY/Vision 的类型兼容闭环修改，不回移 sing-box 1.12 的其他新功能。

## 修改文件

1. `common/tls/utls_client.go`
   - `github.com/sagernet/utls` → `github.com/metacubex/utls`

2. `common/tls/reality_client.go`
   - 切换到 MetaCubeX/uTLS。
   - 移除 `X25519MLKEM768` 的 SupportedCurves/KeyShare 项后重新构建握手状态。
   - 适配 `State13.KeyShareKeys.Ecdhe`。

3. `common/badtls/read_wait_utls.go`
   - 切换 import 和两条 `go:linkname` 路径。
   - 同时识别 `*utls.UConn` 与 Reality 服务端使用的 `*utls.Conn`。
   - 保留 `common.Cast`，以兼容 1.11.15 的 TLS wrapper/Upstream 链。

4. `common/tls/reality_server.go`
   - `sagernet/reality` → MetaCubeX/uTLS 内置 `RealityConfig`、`RealityServer`、`Conn`。
   - 构建条件改为 `with_reality_server && with_utls`。

5. `common/tls/reality_stub.go`
   - 对应调整构建条件，避免只开 `with_reality_server` 时重复定义或缺少实现。

6. `go.mod`
   - `sagernet/utls v1.6.7` → `metacubex/utls v1.8.4`
   - `sing-vmess v0.2.3` → `v0.2.4`（Vision 对新 uTLS 类型的最低兼容版本）
   - `sing v0.6.10` → `v0.6.11`（由 sing-vmess v0.2.4 要求）
   - 同步 MetaCubeX/uTLS v1.8.4 的 Go 1.20 兼容依赖：`x/crypto v0.33.0`、指定 `x/exp`、`compress v1.17.9`。
   - 移除独立 `sagernet/reality`。

## 必须重新生成校验文件

在仓库根目录运行：

```bash
go mod tidy
go mod verify
```

测试子模块还保留旧模块的间接记录，应继续运行：

```bash
cd test
go mod tidy
go mod verify
cd ..
```

提交根目录和 `test/` 下更新后的 `go.mod`、`go.sum`。

## 编译验证

完整 CLI/服务端：

```bash
go build -trimpath \
  -tags "with_utls,with_reality_server" \
  -o sing-box ./cmd/sing-box
```

Android/libbox 构建通常只启用 `with_utls`，不编译 Reality 服务端文件；但仓库完整构建仍应保留服务端迁移。

检查最终二进制：

```bash
go version -m ./sing-box | grep -E "metacubex/utls|sagernet/utls|sagernet/reality|sing-vmess"
```

预期包含：

```text
github.com/metacubex/utls v1.8.4
github.com/sagernet/sing-vmess v0.2.4
```

不应包含：

```text
github.com/sagernet/utls
github.com/sagernet/reality
```
