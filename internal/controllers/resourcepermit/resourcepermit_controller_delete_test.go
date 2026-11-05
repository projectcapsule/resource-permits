// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package resourcepermit

import (
	"context"
	"fmt"
	"testing"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	permitmeta "github.com/projectcapsule/resource-permits/pkg/api/meta"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8smeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
	"github.com/projectcapsule/capsule/pkg/api/meta"
	apiruntime "github.com/projectcapsule/capsule/pkg/api/runtime"
	"github.com/projectcapsule/capsule/pkg/runtime/configuration"
	"github.com/projectcapsule/capsule/pkg/runtime/ssa"
)

func TestResourcePermitWaitsForManagedResourceDeletion(t *testing.T) {
	t.Setenv(configuration.EnvironmentServiceaccountName, "controller")
	t.Setenv(configuration.EnvironmentControllerNamespace, "operations")

	for _, deleting := range []bool{false, true} {
		for _, tracked := range []bool{false, true} {
			for _, clusterScoped := range []bool{false, true} {
				t.Run(fmt.Sprintf("deleting=%t/tracked=%t/cluster=%t", deleting, tracked, clusterScoped), func(t *testing.T) {
					ctx := context.Background()
					scheme := runtime.NewScheme()
					require.NoError(t, corev1.AddToScheme(scheme))
					require.NoError(t, rbacv1.AddToScheme(scheme))
					require.NoError(t, capsulev1beta2.AddToScheme(scheme))
					require.NoError(t, permitsv1beta2.AddToScheme(scheme))
					namespace := &corev1.Namespace{Name: "permit-test"}
					permit := &permitsv1beta2.ResourcePermit{
						Name: "permit", Namespace: namespace.Name, UID: "permit-uid",
						Finalizers: []string{meta.ControllerFinalizer},
						Status:     permitsv1beta2.ResourcePermitStatus{Phase: permitsv1beta2.ResourcePermitPhaseExpired},
					}
					if deleting {
						now := metav1.Now()
						permit.DeletionTimestamp = &now
						permit.Status.Phase = permitsv1beta2.ResourcePermitPhaseActive
						namespace.Status.Phase = corev1.NamespaceTerminating
					}
					target := &unstructured.Unstructured{}
					target.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
					target.SetNamespace(namespace.Name)
					mapper := k8smeta.NewDefaultRESTMapper(nil)
					mapper.Add(target.GroupVersionKind(), k8smeta.RESTScopeNamespace)
					if clusterScoped {
						target.SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("ClusterRole"))
						target.SetNamespace("")
						mapper.Add(target.GroupVersionKind(), k8smeta.RESTScopeRoot)
					}
					target.SetName("managed-target")
					target.SetFinalizers([]string{"test.projectcapsule.dev/hold"})
					target.SetLabels(map[string]string{
						meta.CreatedByCapsuleLabel:   permitmeta.ValueControllerResourcePermit,
						meta.ProtectedByCapsuleLabel: permitmeta.ValueControllerResourcePermit,
					})
					target.SetManagedFields([]metav1.ManagedFieldsEntry{
						{Manager: permitmeta.ResourcePermitFieldOwner(permit), Operation: metav1.ManagedFieldsOperationApply,
							APIVersion: target.GetAPIVersion(), FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{
								Raw: []byte(`{"f:metadata":{"f:labels":{"f:projectcapsule.dev/protected-by":{}}}}`),
							}},
						{Manager: meta.ResourceControllerFieldOwnerPrefix(), Operation: metav1.ManagedFieldsOperationUpdate,
							APIVersion: target.GetAPIVersion(), FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{
								Raw: []byte(`{"f:metadata":{"f:labels":{"f:projectcapsule.dev/created-by":{}}}}`),
							}},
					})
					permit.Status.Request = &permitsv1beta2.ResourcePermitStatusRequest{
						Impersonation: controllerExecutorReference(),
						Resources:     []apiruntime.RenderedResource{{Targets: []runtime.RawExtension{{Object: target.DeepCopy()}}}},
					}
					manager := ssa.Manager{Mapper: mapper}
					if tracked {
						item, err := managedResourceStatus(manager, target)
						require.NoError(t, err)
						item.Created = true
						permit.Status.ProcessedItems.UpdateItem(item)
					}
					cl := fake.NewClientBuilder().WithScheme(scheme).
						WithStatusSubresource(permit).WithObjects(namespace, permit, target).WithReturnManagedFields().Build()
					r := &ResourcePermitReconciler{Client: cl, resources: manager}
					key := client.ObjectKeyFromObject(permit)

					for range 2 {
						current := &permitsv1beta2.ResourcePermit{}
						require.NoError(t, cl.Get(ctx, key, current))
						result, err := r.reconcile(ctx, ctrl.Log, current)
						require.NoError(t, err)
						require.Positive(t, result.RequeueAfter)
						require.NoError(t, cl.Get(ctx, key, current), "permit must outlive its terminating resource")
						require.True(t, controllerutil.ContainsFinalizer(current, meta.ControllerFinalizer))
						require.Len(t, current.Status.ProcessedItems, 1)
						require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(target), target))
						require.False(t, target.GetDeletionTimestamp().IsZero())
					}

					target.SetFinalizers(nil)
					require.NoError(t, cl.Update(ctx, target))
					current := &permitsv1beta2.ResourcePermit{}
					require.NoError(t, cl.Get(ctx, key, current))
					_, err := r.reconcile(ctx, ctrl.Log, current)
					require.NoError(t, err)
					require.True(t, apierrors.IsNotFound(cl.Get(ctx, key, current)))
				})
			}
		}
	}
}

func TestResourcePermitDeletionDoesNotPruneUnappliedPreview(t *testing.T) {
	t.Setenv(configuration.EnvironmentServiceaccountName, "controller")
	t.Setenv(configuration.EnvironmentControllerNamespace, "operations")

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, capsulev1beta2.AddToScheme(scheme))
	require.NoError(t, permitsv1beta2.AddToScheme(scheme))
	namespace := &corev1.Namespace{Name: "permit-test"}
	target := &corev1.ConfigMap{
		Name: "existing", Namespace: namespace.Name,
		Data: map[string]string{"existing": "preserve"},
	}
	permit := &permitsv1beta2.ResourcePermit{
		Name: "permit", Namespace: namespace.Name, Finalizers: []string{meta.ControllerFinalizer},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseExpired,
			Request: &permitsv1beta2.ResourcePermitStatusRequest{
				Impersonation: controllerExecutorReference(),
				Resources: []apiruntime.RenderedResource{{Targets: []runtime.RawExtension{{Object: &corev1.ConfigMap{
					APIVersion: "v1", Kind: "ConfigMap",
					ObjectMeta: target.ObjectMeta,
					Data:       map[string]string{"requested": "never-applied"},
				}}}}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(permit).
		WithObjects(namespace, permit, target).WithReturnManagedFields().Build()
	r := &ResourcePermitReconciler{Client: cl}
	current := &permitsv1beta2.ResourcePermit{}
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(permit), current))
	_, err := r.reconcile(ctx, ctrl.Log, current)
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(permit), current)))
	remaining := &corev1.ConfigMap{}
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(target), remaining))
	require.Equal(t, target.Data, remaining.Data)
	require.True(t, remaining.DeletionTimestamp.IsZero())
}

func TestResourcePermitExpiryBeforeActivationDoesNotNeedExecutionIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, capsulev1beta2.AddToScheme(scheme))
	require.NoError(t, permitsv1beta2.AddToScheme(scheme))
	permit := &permitsv1beta2.ResourcePermit{
		Name: "failed-preflight", Namespace: "permit-test",
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseExpired,
			Request: &permitsv1beta2.ResourcePermitStatusRequest{
				Impersonation: &meta.NamespacedRFC1123ObjectReferenceWithNamespace{
					Name: "unavailable", Namespace: "permit-test",
				},
				Resources: []apiruntime.RenderedResource{{Targets: []runtime.RawExtension{{Object: &corev1.ConfigMap{
					APIVersion: "v1", Kind: "ConfigMap",
					Name: "never-applied", Namespace: "permit-test",
				}}}}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(permit).WithObjects(permit).Build()
	r := &ResourcePermitReconciler{Client: cl}
	current := &permitsv1beta2.ResourcePermit{}
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(permit), current))
	_, err := r.reconcile(ctx, ctrl.Log, current)
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(permit), current)))
}

func TestResourcePermitCleanupPreservesUnresolvedReplicationOwnership(t *testing.T) {
	t.Setenv(configuration.EnvironmentServiceaccountName, "controller")
	t.Setenv(configuration.EnvironmentControllerNamespace, "operations")

	for _, deleting := range []bool{false, true} {
		for _, policy := range []apiruntime.ResourceDeletionPolicy{apiruntime.ResourceDeletionPolicyRemove, apiruntime.ResourceDeletionPolicyOrphan} {
			t.Run(fmt.Sprintf("deleting=%t/policy=%s", deleting, policy), func(t *testing.T) {
				scheme := runtime.NewScheme()
				require.NoError(t, corev1.AddToScheme(scheme))
				require.NoError(t, permitsv1beta2.AddToScheme(scheme))
				namespace := &corev1.Namespace{Name: "team-a"}
				permit := &permitsv1beta2.ResourcePermit{
					Name: "permit", Namespace: namespace.Name, UID: "permit-uid", Finalizers: []string{meta.ControllerFinalizer},
					Status: permitsv1beta2.ResourcePermitStatus{Phase: permitsv1beta2.ResourcePermitPhaseExpired},
				}
				if deleting {
					now := metav1.Now()
					permit.DeletionTimestamp = &now
					namespace.Status.Phase = corev1.NamespaceTerminating
				}
				target := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]any{"external": "preserve"}}}
				target.SetName("shared")
				target.SetNamespace(namespace.Name)
				target.SetUID("shared-uid")
				target.SetLabels(map[string]string{permitmeta.ResourcePermitProtectionLabel: meta.ValueTrue, meta.ReplicationProtectionLabel: meta.ValueTrue})
				target.SetManagedFields([]metav1.ManagedFieldsEntry{
					{Manager: permitmeta.ResourcePermitFieldOwner(permit), Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1", FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:external":{}}}`)}},
					{Manager: "abc/default/tenant/0/raw-0/", Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1", FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:external":{}}}`)}},
				})
				permit.Status.Request = &permitsv1beta2.ResourcePermitStatusRequest{
					Impersonation: controllerExecutorReference(),
					Resources:     []apiruntime.RenderedResource{{Policy: apiruntime.ResourceTemplatePolicy{Deletion: policy}, Targets: []runtime.RawExtension{{Object: target.DeepCopy()}}}},
				}
				mapper := k8smeta.NewDefaultRESTMapper(nil)
				mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), k8smeta.RESTScopeNamespace)
				manager := ssa.Manager{Mapper: mapper}
				item, err := managedResourceStatus(manager, target)
				require.NoError(t, err)
				permit.Status.ProcessedItems.UpdateItem(item)
				cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(permit).
					WithObjects(namespace, permit, target).WithReturnManagedFields().WithInterceptorFuncs(interceptor.Funcs{
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						t.Fatal("cleanup must not discover Capsule owners by listing resources")
						return nil
					},
					Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
						t.Fatal("unresolved ownership must not patch the target")
						return nil
					},
					Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
						t.Fatal("unresolved ownership must not delete the target or permit")
						return nil
					},
				}).Build()
				before := &unstructured.Unstructured{}
				before.SetGroupVersionKind(target.GroupVersionKind())
				require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(target), before))
				r := &ResourcePermitReconciler{Client: cl, resources: manager}
				for range 2 {
					current := &permitsv1beta2.ResourcePermit{}
					require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(permit), current))
					_, err = r.reconcile(t.Context(), ctrl.Log, current)
					require.ErrorContains(t, err, "replication owner resolver is not configured")
					require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(permit), current))
					require.Contains(t, current.Finalizers, meta.ControllerFinalizer)
					require.Len(t, current.Status.ProcessedItems, 1)
					require.Equal(t, metav1.ConditionFalse, current.Status.ProcessedItems[0].Status)
					require.Contains(t, current.Status.ProcessedItems[0].Message, "replication owner resolver is not configured")
					require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(target), target))
					require.Equal(t, before.Object, target.Object)
				}
			})
		}
	}
}
