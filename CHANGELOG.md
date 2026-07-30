# Changelog

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
