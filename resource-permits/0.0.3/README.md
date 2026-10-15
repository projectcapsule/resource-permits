# Resource Permits for Headlamp

This plugin provides the top-level **Permits** menu with **Requests** and
**Templates**. It includes request creation, JSON Schema forms, review, retry,
expiry, audit history, notifications, live managed-resource inventory, and SSA
inspection.

The plugin uses `addons.projectcapsule.dev/v1beta2` directly for ResourcePermit,
ResourcePermitTemplate, and GlobalResourcePermitTemplate. Install the Resource
Permits addon and grant the signed-in Headlamp identity the required API access.
Namespaced requests honor Headlamp's Namespace filter; each template API remains
independent so a denied global list does not hide readable namespaced templates.
Approval and lifecycle authorization remain enforced by the backend.

The plugin runs independently or alongside the Capsule plugin. Canonical Custom
Resources links open the same rich pages as the menu. Existing
`/capsule/resource-permits/` bookmarks remain supported. Terminal alternatives use
`kubectl resource-permits review`, `retry`, and `expire`.

Use Node 24 and the locked npm dependencies:

```sh
npm ci
npm run lint
npm run format -- --check
npm run tsc
npm test
npm run build
npm run package
```

The build produces `dist/main.js`; packaging produces a Headlamp plugin archive.
The Dockerfile packages the bundle under `/plugins/resource-permits` for an init
container. Build the bundle before building that image. Headlamp's plugin settings
include shareable section links, disabled by default on Desktop.

### ResourcePermit template catalog metadata

Open **Permits** for Requests and Templates. New requests use a generated
name prefix by default, accept an optional Reason, and open their detail page
immediately after creation. Reviews highlight archive retention or immediate
deletion after expiry. Denied details retain the future Archiving marker even
when the controller has not yet reported a deletion date.

This UI uses the addon's `addons.projectcapsule.dev/v1beta2` ResourcePermit APIs:
`resourcepermits`, `resourcepermittemplates`, and `globalresourcepermittemplates`.
Install the matching CRDs and update applicable Capsule Proxy policies when
upgrading the backend. CLI snippets use `kubectl resource-permits`.

Both namespaced `ResourcePermitTemplate` and cluster-scoped `GlobalResourcePermitTemplate` use the
same icon and description annotations as Tenants. The plugin shows them in template tables,
template details, and the first step of the New ResourcePermit flow. That flow combines readable
local and global templates, provides free-text search and a multi-select tag filter, and requires
every selected tag to match. Template cards are grouped by the first declared tag, while remaining
tags remain filterable. Templates without tags appear under **Uncategorized**. Templates also
support the shared tags annotation. In both Setup and Template Parameters, **View YAML** optionally
opens the exact current ResourcePermit manifest in an application dialog where it can be copied or
downloaded without creating the request:

```yaml
apiVersion: addons.projectcapsule.dev/v1beta2
kind: ResourcePermitTemplate
metadata:
  name: production-diagnostics
  namespace: solar-prod
  annotations:
    info.projectcapsule.dev/icon: mdi:shield-search
    info.projectcapsule.dev/description: Temporary read access for production diagnostics
    info.projectcapsule.dev/tags: production, diagnostics, break-glass
spec:
  approvals:
    approvers:
      - kind: Group
        name: production-approvers
  defaultDuration: 1h
  maxDuration: 4h
  resources:
    - policy:
        creation: Owner
        deletion: Remove
        protect: true
      targets:
        - apiVersion: rbac.authorization.k8s.io/v1
          kind: Role
          metadata:
            name: production-diagnostics
          rules:
            - apiGroups: ['']
              resources: [pods, pods/log]
              verbs: [get, list]
```

Icon values may be Iconify names, Font Awesome classes, or safe HTTP(S)/relative image URLs,
including Iconify names such as `mdi:shield-search`.

### EventHub

The permit bell in Headlamp's top bar opens the live, user-specific **EventHub**. It resolves the
signed-in Kubernetes username and groups with `SelfSubjectReview`. Requestors receive only
`Active`, `Approved`, `Denied`, and `Expired` transitions for their own ResourcePermits. Explicitly
configured manual reviewers receive only the `Requested` transition. Each entry includes its
message, actor, occurrence time, and a link to the request; an actionable review request also
offers the existing right-side **Review** activity.

Only the latest appended transition for each ResourcePermit is considered. The relative-timeframe
selector at the top filters both audiences to the last hour, 24 hours, 7 days, 30 days, or all
time. The default is 24 hours. The adjacent event-type selector shows all events, only **Action
required** reviewer requests, or only **Informational** requestor transitions. Actor identities
link to the corresponding Subject activity.

The EventHub first lists ResourcePermits across all Namespaces permitted by Headlamp, independently
of the page's current Namespace filter, so reviewers do not miss actionable requests outside the
selected scope. If that all-Namespaces request is forbidden, it falls back to the active Namespace
filter or the Permits `?namespace=` query for restricted Tenant users. RBAC and list failures are
displayed in the feed instead of being presented as an empty result.

#### Kubernetes resource form fields

A template parameter can use Capsule's `x-capsule-form` extension inside its JSON Schema 2020-12
schema to load choices from an arbitrary Kubernetes GVK. Headlamp discovers the Kind's API
resource, lists it with the signed-in user's permissions, and stores the rendered `valueTemplate`
when the user selects the rendered `labelTemplate`:

```yaml
spec:
  paramSchema:
    type: object
    required: [clusterRole]
    properties:
      clusterRole:
        type: string
        description: ClusterRole to distribute temporarily
        x-capsule-form:
          widget: kubernetes-resource
          source:
            apiVersion: rbac.authorization.k8s.io/v1
            kind: ClusterRole
            labelSelector: projectcapsule.dev/eligible=true
          option:
            labelTemplate: '{{ .metadata.name }}'
            valueTemplate: '{{ .metadata.name }}'
```

Put the extension on an array's item schema to render a multi-select whose submitted value remains
a JSON string array:

```yaml
spec:
  paramSchema:
    type: object
    required: [clusterRoles]
    properties:
      clusterRoles:
        type: array
        minItems: 1
        uniqueItems: true
        items:
          type: string
          x-capsule-form:
            widget: kubernetes-resource
            source:
              apiVersion: rbac.authorization.k8s.io/v1
              kind: ClusterRole
```

The extension is also discovered through nested objects and arrays, composition/conditional
keywords, and local `$defs`/`$ref` references. Other schema behavior—including required fields,
defaults, enums, item bounds, uniqueness, and `oneOf`/`anyOf`/`allOf`—continues to be handled by
the JSON Schema form and Ajv 2020 validator. `x-kubernetes-validations` is retained as an opaque
server-side keyword; admission validation errors are displayed if Kubernetes rejects the request.

For namespaced GVKs, `source.namespace` may be `request`, `*`, or a literal Namespace. Omitting it
uses the ResourcePermit Namespace; cluster-scoped GVKs are always listed at cluster scope. Optional
`labelSelector` and `fieldSelector` values are forwarded to Kubernetes. Both option templates
default to `{{ .metadata.name }}` and may combine static text with safe object paths, for example
`{{ .metadata.name }} ({{ .metadata.namespace }})`. Discovery and list failures—including RBAC
denials—are shown on the field without breaking the remaining form.

### Shared development toolchain

From the repository root, run `devbox run -- task headlamp:install headlamp:check`.
`task headlamp:package` writes the archive to `dist/headlamp/`, preserving existing
archives in the plugin source directory.

### Release automation

Pushing a `v*` semantic-version tag runs one GoReleaser job for the controller,
kubectl plugin, and Headlamp plugin. The release hook runs
`task headlamp:release-build` to check and build the plugin, then versions only
`headlamp/dist/package.json`. Source package files remain unchanged.

GoReleaser packages `resource-permits-<version>.tar.gz` containing
`headlamp/main.js`, `headlamp/package.json`, and `headlamp/LICENSE`. The archive
is included in checksums, SBOM generation, and Cosign signing. Archives, SBOMs,
and checksums receive `.sigstore.json` bundles before the GitHub release is
published, supporting immutable releases. Image and OCI Helm chart workflows
also sign their published digests with Cosign.

After GoReleaser succeeds, the release workflow calls the pinned shared
`headlamp-publish` action with its `archive` input. The action verifies the
released archive's checksum, then publishes this README and Artifact Hub metadata
under `resource-permits/<version>/` on `headlamp-plugin`, without rebuilding or
uploading release assets. Release jobs are serialized across tags. Pull requests continue
to run the separate Headlamp checks with read-only permissions; publishing runs
only on tag pushes.
