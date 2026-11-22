---
title: Temporary configuration
weight: 10
description: Define a namespaced template, request a temporary ConfigMap, review it, and observe cleanup.
aliases:
  - /docs/guides/examples/temporary-config/
---

A platform operator offers a ConfigMap with a validated message. A team member
requests an instance for 20 minutes, and a reviewer approves the rendered resource.
The controller creates it, protects it while active, and removes it when the permit
expires. The permit remains for seven days as an audit record.

This is the pair shown in the [interactive homepage example]({{< relref "/#try-a-permit" >}}).

## Prerequisites

[Install Resource Permits]({{< relref "/docs/getting-started/installation" >}}).
This example works without Capsule. Use an existing namespace named `solar-system`,
or create it with `kubectl create namespace solar-system`. Change the namespace
consistently in all manifests if needed. The requester must be authorized to create and read
ResourcePermits there. The reviewer needs read access and permission to update
`resourcepermits/status`, as well as membership in `platform-on-call`.

The combined manifest below includes the executor ServiceAccount and its namespace-scoped
ConfigMap permissions. Apply it as the platform operator. Listing an approver in a
template does not grant Kubernetes RBAC.

When using Capsule, its [namespace assignment](https://projectcapsule.dev/docs/tenants/namespaces/)
and [tenant permissions](https://projectcapsule.dev/docs/tenants/permissions/) guides
explain how to prepare the tenant namespace and its users.

## Define the template

The template accepts one `message` parameter. It uses the permit's name for the
ConfigMap name and the authenticated requester for `requestedBy`. Namespaced targets
without an explicit namespace are placed in the permit namespace.

{{< example "platform/resourcepermittemplates/temporary-config.yaml" >}}

Apply the combined executor permissions and template, then wait for template readiness:

```sh
kubectl apply -f playground/manifests/platform/resourcepermittemplates/temporary-config.yaml
kubectl wait --for=condition=Ready resourcepermittemplate/temporary-config -n solar-system --timeout=60s
```

See [template scope and readiness]({{< relref "/docs/concepts/templates#template-readiness" >}})
and [resource policies]({{< relref "/docs/concepts/managed-resources#resource-policy" >}}).

## Request an instance

Submit this as the requesting team member. The reference resolves to the template
in the same namespace. Its requested 20-minute duration is below the two-hour limit.

{{< example "user/solar/resourcepermits/temporary-config.yaml" >}}

```sh
kubectl create -f playground/manifests/user/solar/resourcepermits/temporary-config.yaml
kubectl get resourcepermit investigate-checkout -n solar-system -o yaml
```

Wait for phase `Requested`, then inspect `.status.request.resources`: those are the
concrete manifests the reviewer will approve. An empty `message`, an unknown
parameter, or a duration above `2h` is invalid. A request cannot use this namespaced
template from a different namespace.

## Review and observe cleanup

With the [kubectl plugin]({{< relref "/docs/reference/cli" >}})
installed, a member of `platform-on-call` reviews the permit:

```sh
kubectl resource-permits review investigate-checkout -n solar-system
```

Approve the request in the interactive review. Without a future start time, it can
activate immediately. Once its phase is `Active`, inspect the managed object:

```sh
kubectl get configmap investigate-checkout -n solar-system -o yaml
```

Its `data.message` matches the request and `data.requestedBy` identifies the
requester. On expiry, the controller removes the ConfigMap according to
`deletion: Remove`. The permit transitions to `Expired` and remains for `keepFor: 7d`.
An authorized operator can also [expire it early]({{< relref "/docs/concepts/requests#review-and-control-a-permit" >}}).
