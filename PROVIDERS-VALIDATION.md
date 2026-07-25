# Providers backport validation

## Static validation performed in the generation environment

- All Go source files were parsed by `gofmt`.
- Modified files pass `git diff --check`.
- GitHub Actions workflow YAML files were parsed successfully.
- No AnyTLS implementation was included in Provider code.
- Internal sing-box call sites use the Provider registry when creating a configuration context.
- The Provider parser is restricted to outbound types present in sing-box 1.11.15.
- Cache restoration was audited to prevent repeated provider-tag prefixing.

## Online CI validation included

Run the workflow:

```text
Providers backport check
```

It performs:

1. `go mod download` and `go mod verify`.
2. `go mod tidy` and a module-file diff check.
3. Provider parser and adapter unit tests.
4. Compilation checks for local/remote Providers, groups, cache file, and Clash API.
5. A full tagged command-line build.
6. A real `sing-box check` using an inline Provider and selector.

## Recommended runtime regression tests

- Remote Provider initial download through `download_detour: direct`.
- HTTP 304 / ETag refresh.
- Restart with cache file enabled and verification that tags remain `provider/node` rather than `provider/provider/node`.
- Local file modification and group refresh.
- Selector manual selection persistence through Clash API/cache file.
- URLTest Provider node addition, removal, and replacement.
- VLESS + REALITY + Vision node imported from sing-box JSON.
- VLESS, VMess, Trojan, Hysteria2, TUIC, and Shadowsocks nodes imported from Clash YAML.
- Raw/Base64 URI subscription import.
- Provider update failure while old nodes remain operational.

## Validation limitation

The artifact-generation container cannot resolve external Go module hosts, so it cannot complete the final linked build locally. The included GitHub Actions workflow is the authoritative online compilation check.
