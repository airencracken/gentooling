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

World and system selections are sorted by their rendered value. Duplicate
world entries are collapsed. Snapshot consistency means shared VDB/world locks
were held and two complete observations agreed; it does not claim that
administrator configuration files participate in Portage's transaction lock.

Visibility outcomes, stability, effective keyword ordering, mask/unmask
precedence, and evidence ordering are public contracts. An empty matching
`package.accept_keywords` rule means the host testing keyword. Explicit command
environment `ACCEPT_KEYWORDS` resets disk policy before applying its values.

Repository locations are interpreted relative to explicit `SystemPaths.Root`,
and discovered repositories are ordered masters before children. Snapshot
consistency modes are validated public evidence: locked mode never degrades to
lockless mode implicitly.

Kernel requirements are conservative evidence. Static requirements and dynamic
shell expressions are separate public contracts; Gentooling does not execute
ebuild or eclass code to convert dynamic evidence into a claim. Kernel-module
rebuild state is evaluated only against an explicit target release.

Candidate visibility and USE evaluation performed through `SystemSnapshot` is
bound to the candidates and policy retained in that stabilized snapshot. It
never performs an implicit live repository lookup.
