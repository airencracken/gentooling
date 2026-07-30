# gentooling

Reusable Go libraries for Gentoo system and package tooling.

The initial API provides explicit system paths, stable package identities, and
read-only installed-package inventory. Inventory scans support partial
inspection with typed diagnostics and strict evidence validation for consumers
that must not act on incomplete package-manager state.

```go
paths := gentooling.DefaultSystemPaths("/")
inventory, err := gentooling.ReadInstalled(ctx, paths, gentooling.InstalledOptions{
    Integrity: gentooling.RequireComplete,
})
```

Gentooling is an interoperable library. It does not execute, replace, or modify
Portage and its surrounding tools.
