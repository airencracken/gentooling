# Changelog

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
