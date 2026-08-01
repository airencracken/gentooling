# Changelog

## v0.10.2

- Treats a single leading `CONFIG_CHECK="${CONFIG_CHECK} ..."` expansion as a
  bounded append while rejecting additional or non-leading self references.
- Adds the explicit `buildonly` merge type and evaluates quoted, boolean
  `MERGE_TYPE` comparisons used by bubblewrap and Chromium's eclass.

## v0.10.1

- Evaluates `MERGE_TYPE` source/binary guards around kernel checks.
- Accepts combined `declare` flags on static associative arrays, including
  Python's `declare -rgA` form, and normalizes quoted associative-array keys.
- Treats an empty local `CONFIG_CHECK` declaration as the reset preceding a
  bounded append loop.

## v0.10.0

- Adds bounded, phase-aware kernel requirement evaluation at actual
  `linux-info_pkg_setup` and `check_extra_config` invocations.
- Evaluates active and inactive USE branches, boolean USE expressions,
  assignment replacement and append flow, and explicit target-kernel version
  predicates without executing Bash.
- Supports quoted multiline and local `CONFIG_CHECK` assignments, static
  wrapper dispatch, and bounded literal arrays and loops.
- Separates operator explanations from developer evidence and classifies
  inactive and warning-only uncertainty as nonblocking.
- Removes synthetic unresolved findings caused solely by inheriting
  `linux-info`.

## v0.9.1

- Adds installed `REQUIRED_USE` to `InstalledPackage` using the same optional,
  symlink-safe, mutation-checked evidence contract as other VDB metadata.

## v0.9.0

- Adds conservative structured Kconfig requirements from ebuild and inherited
  eclass sources, including warning/fatal checks, disabled-symbol
  requirements, USE conditions, source provenance, and check invocations.
- Preserves shell-dependent `CONFIG_CHECK` behavior as explicit dynamic
  evidence and lets strict consumers reject incomplete evaluation without
  executing package shell code.
- Adds installed out-of-tree kernel-module classification from inherited
  eclasses and VDB-owned module artifacts.
- Adds explicit target-kernel rebuild states without consulting the running
  kernel implicitly.
- Adds REQUIRED_USE and inherited-eclass metadata to repository candidates.
- Optionally includes candidates in stabilized system snapshots and evaluates
  prospective visibility and effective USE solely from captured snapshot
  evidence.

## v0.8.2

- Expands configuration references against the preceding assignment
  environment, matching shell order and treating unset variables as empty.
- Prevents exponential self-expansion for canonical first-layer profile
  assignments such as `VARIABLE="${VARIABLE} value"`.

## v0.8.1

- Accepts lowercase shell variable names used by canonical Gentoo profile
  configuration, including Autoconf cache variables.

## v0.8.0

- Adds deterministic, bounded repository candidate discovery from Portage's
  evaluated metadata cache.
- Exposes candidate version, repository, slot/subslot, EAPI, KEYWORDS,
  structured IUSE defaults, and dependency metadata without executing ebuild
  shell code.
- Applies strict and partial integrity handling to malformed, unreadable,
  symlinked, and concurrently changing repository evidence.
- Accepts Portage's canonical multiline configuration assignments, including
  quoted `FEATURES` blocks and backslash continuations.
- Rejects truncated multiline configuration while retaining assignment-start
  provenance.

## v0.7.0

- Adds root-aware repos.conf discovery with deterministic master-before-child
  ordering, sync metadata, priorities, main-repository identity, and source
  provenance.
- Rebases live-root repository locations beneath explicit alternate roots and
  automatically supplies discovered repositories to profile/config loading.
- Adds explicit `LockedAndStabilized` and `StabilizedLockless` snapshot modes.
- Records the achieved snapshot consistency mode and never silently falls back
  after an unreadable Portage lock.
- Adds typed repository-cycle and unavailable-lock errors.

## v0.6.0

- Adds prospective package visibility evaluation with typed visible,
  package-masked, keyword-masked, and unsupported-architecture outcomes.
- Loads effective global and package-specific keyword policy with ordered
  additions, removals, empty-rule testing acceptance, and provenance.
- Loads repository, active-profile, and user package mask/unmask stacks with
  ordered removal semantics and mask reasons.
- Returns stability and a complete policy evidence trail for consumers such as
  Arise and Maize.
- Validates malformed atoms, keywords, policy files, and symlinked policy
  entries as typed invalid evidence.

## v0.5.0

- Adds typed, deterministic world and effective profile system selections with
  package-atom and named-set distinction.
- Adds consistent combined system snapshots spanning installed packages,
  effective configuration/profile policy, and selections.
- Observes Portage-compatible VDB and world fcntl locks shared by Portage,
  Arise, and cooperating ecosystem tools.
- Requires two agreeing complete observations and reports persistent change as
  `ErrConcurrentMutation`.
- Exposes Portage state-lock path derivation for consumer interoperability.

## v0.4.0

- Adds public Gentoo atom and arbitrary-precision version parsing, rendering,
  and package matching, including slots, repositories, and USE dependencies.
- Adds package-specific effective USE evaluation with ordered provenance,
  IUSE defaults, user and profile policy, stable policy, force, and mask state.
- Adds a stable `make test` entry point covering unit, race, and vet checks.
- Licenses Gentooling under GNU GPL version 3.

## v0.3.0

- Adds explicit effective Portage configuration loading.
- Combines make.globals, ordered profile defaults, user make.conf, package.use,
  and an explicitly supplied command environment.
- Retains global and package USE policy provenance.
- Materializes configured USE_EXPAND values without consulting process state.
- Rejects malformed assignments and symlinked configuration policy.
- Deliberately leaves package atom matching and final USE evaluation to the
  next public carveout.

## v0.2.0

- Adds explicit, root-aware active profile graph loading.
- Preserves root-to-leaf profile layers and package-policy source lines.
- Resolves cross-repository parents only through configured repository roots.
- Rejects profile escapes, cycles, malformed rules, and policy symlinks.
- Supports context cancellation and caller-owned deterministic results.

## v0.1.0

Initial public library release:

- Stable package identities and explicit root-relative system paths.
- Concurrent, deterministic installed-package inventory.
- Strict and partial integrity modes with validated options.
- Typed diagnostics for malformed, interrupted, corrupt, unreadable, and
  concurrently changing VDB evidence.
- Structured IUSE declarations with enabled, disabled, and unspecified
  defaults.
- Symlink-safe metadata reads and opt-in CONTENTS payload loading.
- Pre-1.0 semantic-versioning compatibility policy.
