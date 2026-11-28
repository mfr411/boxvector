# Contributing to boxvector

Contributions to boxvector are always appreciated! Please open an [Issue](https://github.com/mfr411/boxvector/issues/new/choose) for bug reports or a [Pull Request](https://github.com/mfr411/boxvector/compare) for code contributions.

## Code Style

boxvector follows Go's conventional code style. Please run `go fmt` before opening a PR.

## Testing

boxvector relies on Go's [testing](https://pkg.go.dev/testing) package. All new code contributions should have associated unit and integration tests where applicable.

## Repository layout

```
cmd/              CLI commands (cobra): generate, web, agent, monitor, ...
pkg/
  config/         config schema (YAML)          process/    load + validate + generate
  embed/          BIRD templates                bird/       birdc client, version checks
  web/            web UI (Go handlers, templates/, static/)
  monitor/        fleet monitor + agent         irr/ peeringdb/ optimizer/ ...
packaging/
  deb/            .deb build script + maintainer scripts
  systemd/        service units
  examples/       default config + nginx example
docs/             vpp.md (hand-written), cli.md + configuration.md (generated: make docs)
tests/            fixtures and the BIRD version matrix
install.sh        one-shot installer (BIRD, boxvector, optional VPP/web)
```
