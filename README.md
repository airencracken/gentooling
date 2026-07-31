# gentooling

Reusable Go libraries for Gentoo system and package tooling.

The initial API provides explicit system paths, stable package identities, and
read-only installed-package inventory. Inventory scans support partial
inspection with typed diagnostics and strict evidence validation for consumers
that must not act on incomplete package-manager state. Independent package
records are scanned and revalidated with bounded concurrency while results and
diagnostics remain deterministic.

```go
paths := gentooling.DefaultSystemPaths("/")
repositories, err := gentooling.ReadRepositories(ctx, paths)
candidateInventory, err := gentooling.ReadRepositoryCandidates(ctx, repositories,
    gentooling.CandidateOptions{Integrity: gentooling.RequireComplete})
kernelRequirements, err := gentooling.ReadKernelRequirements(ctx,
    candidateInventory.Candidates[0], repositories,
    gentooling.KernelRequirementOptions{Integrity: gentooling.AllowPartial})
modules, err := gentooling.ReadInstalledKernelModules(ctx, paths,
    gentooling.InstalledKernelModuleOptions{
        Integrity: gentooling.RequireComplete,
        TargetKernelRelease: "6.12.31",
    })
inventory, err := gentooling.ReadInstalled(ctx, paths, gentooling.InstalledOptions{
    Integrity: gentooling.RequireComplete,
})

profile, err := gentooling.ReadProfile(ctx, gentooling.SystemPaths{
    ActiveProfile: "/etc/portage/make.profile",
    Repositories: []gentooling.RepositoryPath{
        {Name: "gentoo", Path: "/var/db/repos/gentoo"},
    },
})

config, err := gentooling.ReadEffectiveConfig(ctx, paths, gentooling.ConfigOptions{
    Environment: os.Environ(),
})

atom, err := gentooling.ParseAtom(">=sys-kernel/gentoo-sources-6.12:6.12")
matches, err := atom.Matches(packageID, gentooling.UseState{})

use, err := config.EvaluateUse(ctx, gentooling.PackageContext{
    ID: packageID,
    DeclaredUse: installedPackage.DeclaredUse,
    Stable: true,
})

selections, err := gentooling.ReadSelections(ctx, paths)

snapshot, err := gentooling.ReadSystemSnapshot(ctx, paths, gentooling.SnapshotOptions{
    Installed: gentooling.InstalledOptions{
        Integrity: gentooling.RequireComplete,
    },
    Config: gentooling.ConfigOptions{
        Environment: os.Environ(),
    },
    IncludeCandidates: true,
    Candidates: gentooling.CandidateOptions{
        Integrity: gentooling.RequireComplete,
    },
})

prospective, err := snapshot.EvaluateCandidate(ctx, candidateID)
```

`Environment` is always explicit. Passing `nil` performs a disk-only
evaluation and never imports the process environment.

Atom matching returns `false, nil` for an ordinary mismatch and wraps malformed
input with `ErrInvalidData`. Effective USE evaluation only returns declared
flags, sorts decisions by name, and retains applied evidence in policy
precedence order. Consumers decide whether a package is stable and pass that
decision explicitly.

`ReadSystemSnapshot` observes the Portage-compatible VDB and world fcntl locks
used by Portage and Arise, then requires two consecutive complete observations
to agree. This also detects configuration edits and non-cooperating writers
which do not honor those locks. The call waits for cooperating writers,
respects context cancellation, retries a bounded number of observations, and
returns `ErrConcurrentMutation` rather than a mixed view when state does not
stabilize.

Setting `IncludeCandidates` adds repository metadata to both stabilizing
observations. `SystemSnapshot.EvaluateCandidate` then evaluates visibility and
effective USE together from that captured policy and candidate state. Missing
or ambiguous candidate evidence is an error rather than an implicit lookup
outside the snapshot.

Alternate-root callers use the same absolute locations normally written in
repos.conf; Gentooling rebases them beneath `SystemPaths.Root` and never
consults the corresponding host paths. Repositories are returned
master-before-child and are included in effective configuration and combined
snapshots.

Repository candidate discovery reads Portage's evaluated `metadata/md5-cache`
rather than executing ebuild shell code. It exposes version, repository,
slot/subslot, EAPI, KEYWORDS, structured IUSE defaults, REQUIRED_USE, inherited
eclasses, and dependency metadata. Scans are bounded, deterministic,
symlink-safe, and report malformed, unreadable, or concurrently changing
evidence through the same partial and strict integrity model as
installed-package inventory.

Kernel requirement discovery never sources an ebuild or eclass. It structures
literal `CONFIG_CHECK` symbols, warning/fatal behavior, required-disabled
symbols, USE conditions, source provenance, and `check_extra_config`
invocations. Expressions that require shell evaluation remain explicit
`DynamicKernelEvidence`. `RequireComplete` rejects those unresolved expressions;
`AllowPartial` returns the useful static subset and diagnostics. Consumers must
not treat absence of static requirements as proof that runtime policy is empty.
The runtime dispatch performed by `linux-info.eclass` is itself retained as
dynamic evidence, even when every discovered symbol is literal.

Installed kernel-module inventory combines VDB `INHERITED` metadata with owned
out-of-tree `.ko` artifacts. The target kernel release is explicit and is
compared with the releases embedded in owned module paths. Gentooling never
consults the running kernel implicitly.

Canonical multiline assignments in `make.globals` and `make.conf`, including
quoted `FEATURES` blocks and backslash continuations, retain the source line of
the assignment and reject incomplete input.

`LockedAndStabilized` is the default snapshot guarantee. Unprivileged
inspection may explicitly request `StabilizedLockless`; Gentooling records that
mode in the result and still requires consecutive agreeing observations.
Failure to read a lock returns `ErrStateLockUnavailable` and never triggers an
implicit lockless fallback.

`ReadSelections` independently observes the world lock while reading user
selections. Profile policy remains protected by validation rather than a
package-manager transaction lock because administrator edits do not
participate in that lock protocol.

Prospective visibility evaluation combines the candidate's repository
`KEYWORDS` with effective `ACCEPT_KEYWORDS`, matching
`package.accept_keywords` rules, and repository/profile/user
`package.mask`/`package.unmask` policy. Ordinary rejection is returned as a
typed result with ordered evidence; malformed policy remains an error.

Run the complete validation target with:

```sh
make test
```

Gentooling is an interoperable library. It does not execute, replace, or modify
Portage and its surrounding tools.

See [COMPATIBILITY.md](COMPATIBILITY.md) for the pre-1.0 API policy.

## License

Gentooling is licensed under the GNU General Public License, version 3. See
[LICENSE](LICENSE).

## Acknowledgment

The name Gentooling is inspired by Gentoolkit and acknowledges the established
Gentoo package-tooling ecosystem that made this work possible. Gentooling is an
independent project: it is not affiliated with Gentoolkit and is intended to
work alongside existing Gentoo tools, not replace them.
