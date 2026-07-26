# Providers transactional update and group collection

This backport keeps the sing-box 1.11.15 architecture while making Provider
loading and replacement deterministic.

## Provider startup

A cache entry is usable only when its key matches `provider tag + SHA-256(URL)`
and its content can be parsed, filtered, validated and completely constructed.
A broken matching cache is ignored and the current URL is downloaded
synchronously. If no complete candidate set can be built, Provider startup
returns an error and `Box.Start()` fails.

Remote subscription nodes whose outbound type is not registered in the current
1.11.15 build are skipped with a warning. `direct`, `block`, `dns`, `selector`
and `urltest` entries in a Provider document are also skipped. A retained,
supported outbound with invalid options, an invalid dependency or a creation
failure rejects the complete load. A Provider containing no supported retained
outbound is rejected.

## Runtime updates

Provider nodes are constructed in an isolated staging view. The live outbound
manager is not changed while the candidate set is parsed, dependency-sorted,
constructed and advanced through the current lifecycle stage. A cache write is
also completed before publication. The complete batch is then published under
one manager lock.

Any failure aborts and closes the staged batch. The previous Provider nodes,
cache state, ETag, update time and group membership remain unchanged. Partial
new/old Provider sets are not committed.

After publication, selector and URLTest groups resolve the new objects by tag.
Only then are replaced Provider objects closed. URLTest history for permanently
removed tags is deleted.

## Group automatic collection

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
Automatic static collection uses ordinary outbounds declared in the main
configuration; selector, URLTest and DNS outbounds are not collected
automatically.

Candidate sources and filter scope:

| Candidate source | `include` | `exclude` by default | `exclude_type` by default |
| --- | --- | --- | --- |
| Explicit `outbounds` | bypassed | bypassed | bypassed |
| Automatically collected static outbounds | applied | applied | applied |
| Nodes from explicit/all Providers | applied | applied | applied |

`exclude_all: true` also applies `exclude` to explicit `outbounds`.
`exclude_type_all: true` also applies `exclude_type` to explicit `outbounds`.
Explicit candidates win source classification when the same tag is collected
again automatically. Final membership is de-duplicated by tag.

A group may temporarily have no selected outbound while Providers have not
completed strict initial loading. After all Providers complete the start stage,
Box startup rejects every empty selector or URLTest group. At runtime, an update
that would turn a previously usable dependent group into an empty group is
rejected, leaving the previous Provider and Group state unchanged.
