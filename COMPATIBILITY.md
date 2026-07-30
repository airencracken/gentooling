# Compatibility

Gentooling follows semantic versioning.

Before version 1.0, a minor release may make an incompatible API change when a
real consumer demonstrates that the existing contract is unsafe or
insufficient. Patch releases remain backward compatible within their minor
series.

Incompatible minor releases will document migrations. Stable issue codes,
sentinel errors, serialized field meanings, and integrity guarantees are
treated as API contracts. Consumers should use `errors.Is` and `errors.As`
rather than matching error or issue messages.

Deprecated APIs will normally remain for at least one minor release unless
retaining them would preserve unsafe behavior.

Atom parsing, version comparison, ordinary no-match behavior, deterministic
USE decision ordering, and USE evidence precedence are public compatibility
contracts. Package stability is explicit consumer input; Gentooling does not
silently infer accepted keyword policy.
