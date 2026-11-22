---
title: Managed Grafana
weight: 20
description: Provision a Grafana instance in a tenant namespace through a reviewed permit and Flux.
aliases:
  - /docs/guides/examples/managed-grafana/
---

A tenant requests a Grafana instance. After review, the template creates a new tenant
namespace and a Capsule `GlobalTenantResource` that distributes the Flux repository,
HelmRelease, and execution permissions into it. For tenant `solar` and instance `main`,
the namespace is `solar-grafana-main`.

See [Capsule's GlobalTenantResources documentation](https://projectcapsule.dev/docs/replications/global/)
for how the distributor selects tenant namespaces and reconciles generated resources.
The permit controls the initial provisioning and its own cleanup policy.

## Prerequisites

Install Capsule, Resource Permits, and Flux's source and Helm controllers. Use an
existing namespace `solar-system` owned by tenant `solar`, with the tenant label
and default ServiceAccount. The requester needs ResourcePermit permissions there;
the reviewer needs status-update permission and must match the template's approvers.
Change `kubernetes-admin` and `admin` to your actual reviewer identities.

The template executes as `capsule` in `capsule-system` so Capsule can assign ownership
of the new namespace. Adapt that reference if your Capsule installation uses a
different controller ServiceAccount. The generated distributor uses a separate
`permit-example-reconciler` identity. Install the supporting permissions first:

{{< example "platform/globalresourcepermittemplates/rbac.yaml" >}}

These shared example permissions also include the Gateway API role used by the
[Gateway API walkthrough]({{< relref "/docs/examples/gateway-api-access" >}}).

## Define the template

Choose a new instance name for every request. The template derives the tenant from
the request namespace rather than accepting a tenant name as a parameter. Its
ConfigMap, Secret, and workload permissions belong to the Flux executor in the new
namespace. Grafana persistence is disabled in this demonstration.

{{< example "platform/globalresourcepermittemplates/managed-grafana.yaml" >}}

Apply the supporting permissions and template as a platform operator, then wait:

```sh
kubectl apply -f playground/manifests/platform/globalresourcepermittemplates/rbac.yaml
kubectl apply -f playground/manifests/platform/globalresourcepermittemplates/managed-grafana.yaml
kubectl wait --for=condition=Ready globalresourcepermittemplate/grafana --timeout=60s
```

## Request an instance

{{< example "user/solar/resourcepermits/managed-grafana.yaml" >}}

Submit the permit as the tenant requester. A configured approver reviews it with
`kubectl resource-permits review managed-grafana -n solar-system`. See the
[kubectl plugin reference]({{< relref "/docs/reference/cli" >}}) for installation
and the [review guide]({{< relref "/docs/concepts/requests#review-and-control-a-permit" >}})
for approval options.

## Expected outcome and cleanup

Once the permit is active, Capsule distributes the Flux resources and Flux installs
Grafana. Permit activation and HelmRelease readiness are separate: inspect both
the permit's conditions and the HelmRelease in `solar-grafana-main`.

No default duration is set, so the instance stays managed until the permit is
expired or deleted. Its `deletion: Remove` policy removes the namespace and distributor
on cleanup, including the namespace's contents. Use a dedicated instance namespace.
