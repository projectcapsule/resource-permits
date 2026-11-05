// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"time"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	permitmeta "github.com/projectcapsule/resource-permits/pkg/api/meta"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
	"github.com/projectcapsule/capsule/pkg/api"
	"github.com/projectcapsule/capsule/pkg/api/meta"
	"github.com/projectcapsule/capsule/pkg/api/rbac"
	apiruntime "github.com/projectcapsule/capsule/pkg/api/runtime"
	"github.com/projectcapsule/capsule/pkg/runtime/selectors"

	"github.com/projectcapsule/resource-permits/pkg/api/resourcepermit"
)

func exerciseMixedProtection(global bool, tenantName, baseNamespace, targetNamespace, excludedNamespace string, owner rbac.UserSpec) {
	ctx := context.Background()
	name := fmt.Sprintf("mixed-protection-%t", global)
	target := &corev1.ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Name: name, Data: map[string]string{"shared": "managed"}}
	seed := target.DeepCopy()
	seed.Namespace = targetNamespace
	seed.Data = map[string]string{"external": "retained"}
	Expect(k8sClient.Patch(ctx, seed, client.Apply, client.FieldOwner("e2e-external"))).To(Succeed())
	Expect(k8sClient.Create(ctx, &corev1.ConfigMap{Name: name, Namespace: excludedNamespace, Data: map[string]string{"external": "excluded"}})).To(Succeed())
	common := capsulev1beta2.TenantResourceCommonSpec{ResyncPeriod: resyncPeriod, Resources: []capsulev1beta2.ResourceSpec{{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": targetNamespace}},
		Policy:            &apiruntime.ResourceReplicationPolicy{Creation: apiruntime.ResourceCreationPolicyMerge, Protect: new(false), Deletion: apiruntime.ResourceDeletionPolicyOrphan},
		RawItems:          []capsulev1beta2.RawExtension{{Object: target}},
	}}}
	var parent client.Object
	var spec *capsulev1beta2.TenantResourceCommonSpec
	var status *capsulev1beta2.TenantResourceCommonStatus
	if global {
		obj := &capsulev1beta2.GlobalTenantResource{Name: name, Spec: capsulev1beta2.GlobalTenantResourceSpec{Scope: api.ResourceScopeNamespace, TenantSelector: metav1.LabelSelector{MatchLabels: map[string]string{"example.org/permit-composition": tenantName}}, TenantResourceCommonSpec: common}}
		parent, spec, status = obj, &obj.Spec.TenantResourceCommonSpec, &obj.Status.TenantResourceCommonStatus
	} else {
		// The original TenantResource suite supplied this scoped execution grant
		// in its setup. Keep it explicit in the standalone addon fixture.
		ensureServiceAccount(baseNamespace, name)
		bindServiceAccountToNamespacedResource(baseNamespace, name, targetNamespace, []string{"configmaps"}, []string{"get", "list", "watch", "create", "update", "patch", "delete"})
		obj := &capsulev1beta2.TenantResource{Name: name, Namespace: baseNamespace, Spec: capsulev1beta2.TenantResourceSpec{
			ServiceAccount:           &meta.LocalRFC1123ObjectReference{Name: meta.RFC1123Name(name)},
			TenantResourceCommonSpec: common,
		}}
		parent, spec, status = obj, &obj.Spec.TenantResourceCommonSpec, &obj.Status.TenantResourceCommonStatus
	}
	Expect(k8sClient.Create(ctx, parent)).To(Succeed())
	DeferCleanup(func() { ignoreNotFound(k8sClient.Delete(ctx, parent)) })
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
		g.Expect(status.ProcessedItems).To(HaveLen(1))
		g.Expect(status.ProcessedItems[0].LastApply.IsZero()).To(BeFalse())
		g.Expect(status.ProcessedItems[0].Tenant).To(Equal(tenantName))
		g.Expect(status.ServiceAccount).NotTo(BeNil())
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	sa := status.ServiceAccount
	// This fixture also runs in the TR suite. The GTR helper's shared label
	// would let concurrent GTR cleanup delete the role before permit expiry.
	role := &rbacv1.ClusterRole{
		Name: name, Labels: map[string]string{"env": "e2e"},
		Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"namespaces"}, ResourceNames: []string{targetNamespace}, Verbs: []string{"get"}}},
	}
	Expect(k8sClient.Create(ctx, role)).To(Succeed())
	DeferCleanup(func() { EventuallyDeletion(role) })
	binding := &rbacv1.ClusterRoleBinding{
		Name: name, Labels: map[string]string{"env": "e2e"},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: sa.Name.String(), Namespace: sa.Namespace.String()}},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
	}
	Expect(k8sClient.Create(ctx, binding)).To(Succeed())
	DeferCleanup(func() { EventuallyDeletion(binding) })
	template := &permitsv1beta2.GlobalResourcePermitTemplate{Name: name, Spec: permitsv1beta2.GlobalResourcePermitTemplateSpec{
		Impersonation: resourcePermitServiceAccountReference(sa.Namespace.String(), sa.Name.String()),
		Approvals:     resourcepermit.ApprovalSpec{Auto: true}, DefaultDuration: &metav1.Duration{Duration: 10 * time.Minute},
		NamespaceSelectors: []selectors.NamespaceSelector{{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": targetNamespace}}}},
		Resources:          []apiruntime.ResourceTemplate{{Policy: apiruntime.ResourceTemplatePolicy{Creation: apiruntime.ResourceCreationPolicyMerge, Protect: new(true), Deletion: apiruntime.ResourceDeletionPolicyRemove}, Targets: []runtime.RawExtension{{Object: target}}}},
	}}
	Expect(k8sClient.Create(ctx, template)).To(Succeed())
	DeferCleanup(func() { EventuallyDeletion(template) })
	expectGlobalResourcePermitTemplateNamespaces(ctx, name, targetNamespace)
	actor := impersonationClient(owner.Name, withDefaultGroups([]string{owner.Name}))
	permit := &permitsv1beta2.ResourcePermit{Name: name, Namespace: targetNamespace, Spec: permitsv1beta2.ResourcePermitSpec{Template: permitsv1beta2.ResourcePermitTemplateReference{Kind: permitsv1beta2.GlobalResourcePermitTemplateKind, Name: name}}}
	Expect(actor.Create(ctx, permit)).To(Succeed())
	DeferCleanup(func() { cleanupLifecycleResourcePermit(ctx, permit) })
	// Resolve the deliberately overlapping fixture explicitly. The addon must
	// never discard unknown replication ownership on its own during cleanup.
	executor := impersonationClient(serviceAccountUsername(sa.Namespace.String(), sa.Name.String()), serviceAccountGroups(sa.Namespace.String()))
	resolveSharedTarget := func() {
		ignoreNotFound(k8sClient.Delete(ctx, parent))
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(parent), parent))
		}, defaultTimeoutInterval, defaultPollInterval).Should(BeTrue())
		ignoreNotFound(executor.Delete(ctx, seed))
	}
	DeferCleanup(resolveSharedTarget)
	permit = waitForResourcePermitPhase(ctx, permit, permitsv1beta2.ResourcePermitPhaseActive)
	// Activation must record the existing permit lifecycle state.
	Expect(permit.Status.ProcessedItems).To(HaveLen(1))
	Expect(permit.Status.ProcessedItems[0].LastApply.IsZero()).To(BeFalse())
	By("enabling replication protection on a target already protected by a permit")
	setProtection := func(protect, cordoned bool) {
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(parent), parent); err != nil {
				return err
			}
			spec.Resources[0].Policy.Condition = "false"
			spec.Resources[0].Policy.Protect = new(protect)
			spec.Cordoned = new(cordoned)
			return k8sClient.Update(ctx, parent)
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
			g.Expect(status.ObservedGeneration).To(Equal(parent.GetGeneration()))
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	}
	setProtection(true, false)
	cm := &corev1.ConfigMap{}
	assertPermitProtected := func() {
		// Both controllers label the shared target asynchronously, so the
		// expected denial depends on a state which is only eventually read.
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(seed), cm)).To(Succeed())
			g.Expect(cm.Labels).To(HaveKeyWithValue(permitmeta.ResourcePermitProtectionLabel, meta.ValueTrue))
			g.Expect(cm.Annotations).To(HaveKeyWithValue(permitmeta.ResourcePermitServiceAccountAnnotation, serviceAccountUsername(sa.Namespace.String(), sa.Name.String())))
			before := cm.DeepCopy()
			denial := Or(ContainSubstring("protected by a ResourcePermit"))
			if cm.Labels[meta.ReplicationProtectionLabel] == meta.ValueTrue {
				// Independent validating webhooks have no guaranteed response
				// order, and the replication webhook words its denial
				// differently while its parent is cordoned or deleted.
				denial = Or(denial,
					ContainSubstring("capsule replication "+parent.GetName()),
					ContainSubstring("managing parent is not yet available"),
				)
			}
			g.Expect(actor.Patch(ctx, cm, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"labels":null,"annotations":null}}`)))).To(MatchError(denial))
			g.Expect(actor.Delete(ctx, cm, client.DryRunAll)).To(MatchError(denial))
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(seed), cm)).To(Succeed())
			g.Expect(cm.Labels).To(Equal(before.Labels))
			g.Expect(cm.Annotations).To(Equal(before.Annotations))
			g.Expect(cm.Data).To(Equal(before.Data))
			g.Expect(cm.DeletionTimestamp.IsZero()).To(BeTrue())
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(seed), cm)).To(Succeed())
		g.Expect(cm.Labels).To(HaveKeyWithValue(meta.ProtectedByCapsuleLabel, permitmeta.ValueControllerResourcePermit))
		g.Expect(cm.Labels).To(HaveKeyWithValue(meta.ReplicationProtectionLabel, meta.ValueTrue))
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	setProtection(false, false)
	// Replication protection is removed by a later reconcile of the parent.
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(seed), cm)).To(Succeed())
		g.Expect(cm.Labels).NotTo(HaveKey(meta.ReplicationProtectionLabel))
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	assertPermitProtected()
	setProtection(true, false)
	By("preserving both controllers' protection when replication ownership cannot be resolved")
	setProtection(true, true)
	expireActiveResourcePermit(ctx, permit)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(permit), permit)).To(Succeed())
		g.Expect(permit.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseExpired))
		g.Expect(permit.Finalizers).To(ContainElement(meta.ControllerFinalizer))
		g.Expect(permit.Status.ProcessedItems).To(HaveLen(1))
		g.Expect(permit.Status.ProcessedItems[0].Status).To(Equal(metav1.ConditionFalse))
		g.Expect(permit.Status.ProcessedItems[0].Message).To(ContainSubstring("replication owner resolver is not configured"))
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(seed), cm)).To(Succeed())
		g.Expect(cm.Data).To(Equal(map[string]string{"shared": "managed", "external": "retained"}))
		g.Expect(cm.Labels).To(HaveKeyWithValue(meta.ReplicationProtectionLabel, meta.ValueTrue))
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	assertPermitProtected()

	By("orphaning the replication without clearing surviving permit protection")
	Expect(k8sClient.Delete(ctx, parent)).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	}, defaultTimeoutInterval, defaultPollInterval).Should(BeTrue())
	assertPermitProtected()
	// Orphan retains managed fields. Removing its parent alone is not proof that
	// the addon can safely discard those owners. An authorized executor resolves
	// this test target explicitly after all preservation assertions have passed.
	By("finishing permit cleanup after its executor removes the shared target")
	resolveSharedTarget()
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(permit), permit))
	}, defaultTimeoutInterval, defaultPollInterval).Should(BeTrue())
	expectConfigMapData(excludedNamespace, name, map[string]string{"external": "excluded"})
}

var _ = Describe("Addon protection composition", Label("resource-permit", "replications", "protection-composition"), func() {
	for _, global := range []bool{true, false} {
		It(fmt.Sprintf("composes protection with global=%t", global), func() {
			ctx := context.Background()
			name := "e2e-addon-composition-" + rand.String(6)
			owner := rbac.UserSpec{Name: name, Kind: rbac.UserOwner}
			tnt := &capsulev1beta2.Tenant{Name: name, Labels: map[string]string{"env": "e2e", "example.org/permit-composition": name}, Spec: capsulev1beta2.TenantSpec{Owners: rbac.OwnerListSpec{{Name: owner.Name, Kind: owner.Kind}}}}
			Expect(k8sClient.Create(ctx, tnt)).To(Succeed())
			DeferCleanup(func() { EventuallyDeletion(tnt) })
			TenantReadyTrue(tnt)
			namespaces := []string{name + "-base", name + "-target", name + "-excluded"}
			for i, namespace := range namespaces {
				ns := NewNamespace(namespace, map[string]string{meta.TenantLabel: name})
				NamespaceCreation(ns, owner, defaultTimeoutInterval).Should(Succeed())
				TenantNamespaceReady(tnt, ns, uint(i+1))
			}
			otherName := name + "-other"
			other := &capsulev1beta2.Tenant{Name: otherName, Labels: map[string]string{"env": "e2e"}, Spec: capsulev1beta2.TenantSpec{Owners: rbac.OwnerListSpec{{Name: otherName, Kind: rbac.UserOwner}}}}
			Expect(k8sClient.Create(ctx, other)).To(Succeed())
			DeferCleanup(func() { EventuallyDeletion(other) })
			TenantReadyTrue(other)
			otherNamespace := NewNamespace(otherName+"-target", map[string]string{meta.TenantLabel: otherName})
			NamespaceCreation(otherNamespace, other.Spec.Owners[0].UserSpec, defaultTimeoutInterval).Should(Succeed())
			TenantNamespaceReady(other, otherNamespace, 1)
			isolated := &corev1.ConfigMap{Name: fmt.Sprintf("mixed-protection-%t", global), Namespace: otherNamespace.Name, Data: map[string]string{"external": "other-tenant"}}
			Expect(k8sClient.Create(ctx, isolated)).To(Succeed())
			exerciseMixedProtection(global, name, namespaces[0], namespaces[1], namespaces[2], owner)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(isolated), isolated)).To(Succeed())
			Expect(isolated.Data).To(Equal(map[string]string{"external": "other-tenant"}))
			Expect(isolated.Labels).NotTo(HaveKey(meta.ReplicationProtectionLabel))
			Expect(isolated.Labels).NotTo(HaveKey(permitmeta.ResourcePermitProtectionLabel))
			actor := impersonationClient(owner.Name, withDefaultGroups([]string{owner.Name}))
			Expect(actor.Delete(ctx, isolated, client.DryRunAll)).To(MatchError(ContainSubstring("forbidden")))
		})
	}
})
