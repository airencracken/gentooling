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
})

visibility, err := snapshot.Config.EvaluateVisibility(ctx,
    gentooling.PackageVisibilityContext{
        ID: candidateID,
        Keywords: []string{"amd64", "~arm64"},
    },
)
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
