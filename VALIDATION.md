# Validation status

Completed locally:

- parsed all Go source files with the Go parser;
- `gofmt` verification for the modified TLS and badtls files;
- parsed both `go.mod` files through `go mod edit -json`;
- validated the structure and duplicate keys of both `go.sum` files;
- verified no source or module file retains `github.com/sagernet/utls` or
  `github.com/sagernet/reality`;
- verified REALITY no longer implements `WithSessionIDGenerator` by source
  inspection and added an automated regression test;
- parsed the added GitHub Actions workflow as YAML;
- generated a complete diff against the original 1.11.15 source archive.

Not completed in the packaging environment:

- downloading the full Go module graph;
- linked package tests;
- final multi-tag binary build.

The packaging environment cannot resolve external Go module hosts. The included
`uTLS backport check` GitHub Actions workflow performs those network-dependent
checks on GitHub after the source is pushed.
