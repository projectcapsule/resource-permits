---
title: kubectl plugin
weight: 20
description: Install and use the kubectl resource-permits plugin to review and control ResourcePermits.
---

The `kubectl-resource_permits` binary is a kubectl plugin for reviewing and
controlling [ResourcePermits]({{< relref "/docs/concepts/requests" >}}). kubectl
finds it on `PATH` and invokes it as `kubectl resource-permits`. The binary also
works on its own as `kubectl-resource_permits`.

Every command reads the named ResourcePermit, then sends the requested phase
change as a status merge patch with optimistic locking. Admission webhooks
validate the transition and record reviewer and actor identity. The plugin
never edits the permit spec or the managed resources.

## Install

Each [GitHub release](https://github.com/projectcapsule/resource-permits/releases)
publishes `kubectl-resource_permits_<version>_<os>_<arch>` archives for Linux,
macOS, and Windows on `amd64` and `arm64`. The release also includes
`checksums.txt`, SBOMs, and cosign signature bundles. Extract the binary into a
directory on `PATH`:

```bash
tar -xzf kubectl-resource_permits_<version>_linux_amd64.tar.gz kubectl-resource_permits
install -m 0755 kubectl-resource_permits "$HOME/.local/bin/"
kubectl plugin list
```

To build from a checkout instead, run `task build`. It writes the plugin to `bin/`:

```bash
devbox run -- task build
export PATH="$PWD/bin:$PATH"
```

Check the installed version with `kubectl resource-permits --version`. Release
builds print the tag and commit. Local builds print `dev`.

## Cluster connection

The plugin loads the client configuration in this order:

1. The file in the `KUBECONFIG` environment variable.
2. The in-cluster ServiceAccount configuration, when running inside a Pod.
3. `$HOME/.kube/config`.

It always uses the kubeconfig's current context. The plugin does not support
kubectl's `--kubeconfig`, `--context`, or `--cluster` flags. To target a
different cluster, set `KUBECONFIG` or switch contexts first:

```bash
kubectl config use-context staging
KUBECONFIG=~/.kube/staging kubectl resource-permits review my-permit -n team-a
```

## Global flags

These flags apply to every command.

| Flag | Default | Description |
| --- | --- | --- |
| `-n`, `--namespace` | `default` | Namespace of the ResourcePermit. |
| `--as` | | Username to impersonate. |
| `--as-group` | | Group to impersonate. Repeat it to add more groups. |
| `-v`, `--version` | | Print the plugin version. |
| `-h`, `--help` | | Show help for a command. |

{{< callout context="caution" icon="outline/alert-triangle" >}}
`--namespace` defaults to `default`. It does not use the namespace set in the
kubeconfig context. Pass `-n` whenever the permit is in another namespace.
{{< /callout >}}

### Impersonation

`--as` and `--as-group` work like kubectl impersonation. The admission webhook
then sees the impersonated identity as the reviewer or actor. That identity is
what gets matched against template `approvers` and conditions. You need
Kubernetes RBAC permission to `impersonate` the given users and groups.

```bash
kubectl resource-permits review gateway-editor-incident-1042 \
  -n solar-uat --approve --as alice --as-group platform-on-call --as-group sre
```

When `--as` is set, the groups given with `--as-group` replace any groups from
the kubeconfig. `--as-group` without `--as` fails, unless the kubeconfig
already impersonates a user.

## Commands

| Command | Required phase | Sets phase | Purpose |
| --- | --- | --- | --- |
| [`review`](#review) | `Requested` | `Approved` or `Denied` | Approve or deny a request. |
| [`activate`](#activate) | `Approved` | `Active` | Activate an approved permit now. |
| [`expire`](#expire) | Any | `Expired` | End access early, or withdraw a permit. |
| [`retry`](#retry) | `Failed` (with failure details) | `Retrying` | Retry a failed preflight or activation. |

Each command takes exactly one argument, the ResourcePermit name. `review`
checks the phase before it changes anything. The other commands send the phase
change directly, so admission accepts or rejects it based on the permit's
lifecycle and the caller's permissions. The plugin prints any rejection and
exits with status `1`.

### review

```text
kubectl resource-permits review NAME [flags]
```

Approves or denies a permit in the `Requested` phase. The command fails if:

- the controller hasn't processed the permit yet (its phase is empty),
- the permit isn't in `Requested`, or
- the permit has no rendered request snapshot in `status.request`.

Without `--approve` or `--deny`, the command reviews interactively. It prints
a summary table first: name, namespace, effective duration, requested duration,
and retention (`KeepFor`). A second table lists each rendered manifest next to
its resource-group policy. Then it asks `Approve this request? [y/n]` and
repeats the question until you answer `y` or `n`.

| Flag | Description |
| --- | --- |
| `--approve` | Approve without asking. |
| `--deny` | Deny without asking. Can't be combined with `--approve`. |
| `-m`, `--message` | Review message, stored in `status.review.message`. |
| `--duration` | How long access stays available after activation, for example `30m` or `1h30m`. Overrides the requested duration. |
| `--keep-for` | How long the permit is kept after it ends, for example `14d`. Overrides the requested retention. |
| `--start-time` | Earliest activation time as an RFC3339 timestamp, for example `2026-07-15T14:45:00Z`. |
| `--no-color` | Print the review tables without ANSI colors. |

`--duration` and `--keep-for` accept Go duration units plus `d` (days) and `w`
(weeks), for example `1w2d` or `36h`. The CLI writes the overrides into the
approved request snapshot only when approving. Admission checks them against
the template's limits. A deny ignores them.

When a review conflicts with another update, the command reads the permit again
and retries the patch a bounded number of times.

```bash
# Interactive review
kubectl resource-permits review gateway-editor-incident-1042 -n solar-uat

# Approve with a shorter window and longer retention
kubectl resource-permits review gateway-editor-incident-1042 -n solar-uat \
  --approve --message "Approved for INC-1042" --duration 30m --keep-for 14d

# Approve for a scheduled start
kubectl resource-permits review gateway-editor-incident-1042 -n solar-uat \
  --approve --start-time 2026-12-01T08:00:00Z

# Deny with a reason
kubectl resource-permits review gateway-editor-incident-1042 -n solar-uat \
  --deny -m "Use the read-only template"

# Plain output for logs or non-ANSI terminals
kubectl resource-permits review gateway-editor-incident-1042 -n solar-uat --no-color
```

Approval rules come from the referenced template. Review permission requires
both Kubernetes RBAC on the status subresource and a match against the
template's `approvers`. See
[approval rules]({{< relref "/docs/concepts/requests#review-and-control-a-permit" >}}).

### activate

```text
kubectl resource-permits activate NAME [flags]
```

Requests the `Active` phase for an `Approved` permit. The controller normally
activates an approved permit once its start time arrives. Use `activate` to ask
for activation explicitly. Admission rejects activation from any other phase.

```bash
kubectl resource-permits activate gateway-editor-incident-1042 -n solar-uat
```

### expire

```text
kubectl resource-permits expire NAME [flags]
```

Requests the `Expired` phase from any phase. On an active permit, this ends
access before the approved duration runs out. The controller then removes or keeps the managed resources according
to their [lifecycle policy]({{< relref "/docs/concepts/managed-resources" >}}).
`Expired` is terminal. If RBAC allows the status update, the requester can
expire their own permit.

```bash
kubectl resource-permits expire gateway-editor-incident-1042 -n solar-uat
```

### retry

```text
kubectl resource-permits retry NAME [flags]
```

Requests the `Retrying` phase for a `Failed` permit. Fix the cause recorded in
`.status.failure` first, such as the execution ServiceAccount, RBAC, admission,
or target conflicts. The controller then repeats the failed preflight or
activation using the permit's captured snapshot.

```bash
kubectl get resourcepermit gateway-editor-incident-1042 -n solar-uat \
  -o jsonpath='{.status.failure}'
kubectl resource-permits retry gateway-editor-incident-1042 -n solar-uat
```

### completion

```text
kubectl resource-permits completion bash|zsh|fish|powershell
```

Generates a shell completion script for the `kubectl-resource_permits`
binary. Run `kubectl resource-permits completion <shell> --help` for setup
instructions for each shell.

## Required permissions

All commands `get` the ResourcePermit and `patch` its `resourcepermits/status`
subresource in the `addons.projectcapsule.dev` API group. `--as` and
`--as-group` also need `impersonate` permission on the given users and groups.
Admission enforces the remaining rules: approver lists, approval conditions,
and allowed phase transitions.
