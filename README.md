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
```

Gentooling is an interoperable library. It does not execute, replace, or modify
Portage and its surrounding tools.

See [COMPATIBILITY.md](COMPATIBILITY.md) for the pre-1.0 API policy.

## Acknowledgment

The name Gentooling is inspired by Gentoolkit and acknowledges the established
Gentoo package-tooling ecosystem that made this work possible. Gentooling is an
independent project: it is not affiliated with Gentoolkit and is intended to
work alongside existing Gentoo tools, not replace them.
