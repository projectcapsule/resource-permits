# Development

## Shared toolchain

Devbox pins tools in `devbox.json` and `devbox.lock`. Use `devbox shell` or prefix
Task commands with `devbox run --`. Tasks consume tools from PATH; they do not
download ad hoc binaries or require Make, Python, or pre-commit.

```sh
devbox run -- task --list
devbox run -- task hooks:install
devbox run -- task check
```

`task test` runs unit tests with race detection and writes `coverage.out`.
`task test:benchmarks` exercises benchmark fixtures. E2e is separate because its
Ginkgo suite uses an existing API server. `task build` creates `bin/manager` and
`bin/kubectl-resource_permits`. `task generate manifests` updates DeepCopy code
and chart CRDs. Generated drift checks assume intended files have been staged.

## Development clusters and e2e

`task dev:deploy DEV_CONTEXT=kind-NAME DEV_KIND_CLUSTER=NAME` builds Resource
Permits with ko and installs it directly into an existing Kind cluster. It validates
the context against the Kind cluster before changing it. The default namespace is
`resource-permits-system`; override `DEV_NAMESPACE` for an existing installation.
Install cert-manager first, or supply existing TLS through `DEV_HELM_VALUES`.
The chart creates the addon's own ServiceAccount. Capsule is not installed or required.
`dev:deploy` uses inline CRDs; review CRD upgrades and existing ownership before redeploying API changes.

`task e2e` defaults to standalone mode and verifies lifecycle and denied operations
in a temporary cluster without Capsule CRDs. `task e2e E2E_MODE=capsule` explicitly
installs Capsule and runs tenant and ownership-safety integration tests. CI tests
standalone operation across supported Kubernetes versions and Capsule integration
on the newest tested version. The permanent playground never deletes itself;
only test clusters created by `e2e` are removed automatically.

For manual integration work, `task dev:capsule` with the same context, cluster, and
namespace arguments installs the Capsule dependency resolved by Go for tenant and
protection compatibility tests. The addon does not watch Capsule resources. This
optional setup is never called by the playground or standalone e2e mode.

Go honors an optional local `go.work`, including a Capsule checkout override.
CI resolves the revision in `go.mod`; use `GOWORK=off` when checking that the
published dependency builds independently of your workspace.

`task e2e` creates its own Kind cluster and private kubeconfig. It records controller
logs and reports under `artifacts/` and deletes only its own cluster. Capsule mode
runs parallel non-config and serial config phases; use `E2E_FILTER` to select
locally affected tests, with tests in both phases. CI uses `E2E_FILTER=all` for the
full integration suite. Preserve the seed and artifacts when investigating failures.
Existing clusters and default contexts are never reused or deleted.

`task playground` creates a persistent cluster with Flux and Headlamp. It never
deletes the cluster automatically. `task playground:headlamp` opens the local UI;
stopping it only closes the port forward. Delete the cluster explicitly with Kind.
See [playground/README.md](playground/README.md). Examples live under
`playground/manifests/`; none are implicitly applied.

## Documentation and Headlamp

`task docs:serve` starts Hugo/Doks locally. `task docs:generate` runs the pinned
upstream crdoc CLI against chart CRDs. `task docs:check` checks drift and builds
root/subpath sites with offline link checks. `task docs:test:browser` verifies desktop
and mobile navigation/search against production assets using the Devbox `chromium`
package. Keep it close to the Chromium release targeted by `@playwright/test` in
`docs/package.json`. Permit and shared-concept guides were migrated from the Capsule
website, with addon API groups, CLI commands, metrics, and Helm settings adapted here.

`task headlamp:install headlamp:check` uses the existing plugin dependencies and
lint/format/type/unit/build checks. `task headlamp:package` creates the plugin archive.
The shared Project Capsule action tests PRs. Tag releases use one GoReleaser job
for the controller, kubectl plugin, and Headlamp plugin, including checksums,
SBOMs, and Cosign bundles. After publication, the release workflow calls the shared
`headlamp-publish` action with the published archive to update the `headlamp-plugin`
metadata branch. Plugin code remains under `headlamp/`.

## Images, charts, and releases

`task image:build` produces a local ko archive without a registry push.
`task image:push` explicitly publishes and prints one immutable image reference.
`task image:sign IMAGE_REF=...` signs it with Cosign. `task helm:package` and
`task helm:push` handle the chart; `task helm:docs` generates the chart README
from `README.md.gotmpl`. `task helm:schema` (alias `helm-schema`) generates
`values.schema.json` from values and Helm Docs comments with the pinned Helm
schema plugin. Chart linting and packaging regenerate the schema; `task helm:verify`
checks generated documentation and schema drift. `task release:snapshot` validates
release packaging without publishing. Release SBOMs require Syft; signing requires
Cosign (installed by release workflows). `enterprise:release` and `enterprise:prerelease` accept
`ENTERPRISE_VERSION`; preview them with `DRY_RUN=true`.

`release` and `release:snapshot` default to `RELEASE_PARALLELISM=1` to reduce peak
temporary disk use during cross-compilation. Override it on a runner with enough
space, for example `task release:snapshot RELEASE_PARALLELISM=2`.

CI retains Devbox's Nix-store cache and shares Go module downloads. Go compiler
caches use separate build, test, e2e, and release scopes, including their restore
prefixes, so releases do not restore race-test compiler outputs. ko layers are
cached only by image and e2e jobs. The new cache keys do not restore the previous
combined cache. GitHub's branch/tag cache-access restrictions still apply.
Release and snapshot workflows report free disk space and inodes before setup,
before compilation, and after completion or failure.

GitHub workflows use the same Task targets and pinned shared Headlamp actions.
Enable GitHub Pages with GitHub Actions before the first main-branch docs deploy.
The intended docs URL is `https://resource-permits.addons.projectcapsule.dev/`;
configure its DNS and Pages custom domain before publishing, or override
`DOCS_BASE_URL` for another location. This migration does not publish or change DNS.
Configure `CODECOV_TOKEN`, optional `FOSSA_API_KEY`/`SCORECARD_READ_TOKEN`, and
`HELM_CHARTS_PUSH_TOKEN` as appropriate. Private repositories need explicit
`SLSA_PUBLIC_PROVENANCE=true` to publish provenance to Rekor. Configure GHCR,
Pages and security-analysis permissions for the repository's visibility/plan.
