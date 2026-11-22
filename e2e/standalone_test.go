// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
	"github.com/projectcapsule/capsule/pkg/api/meta"
	apiruntime "github.com/projectcapsule/capsule/pkg/api/runtime"
	"github.com/projectcapsule/capsule/pkg/runtime/selectors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8smeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	"github.com/projectcapsule/resource-permits/pkg/api/resourcepermit"
)

var _ = Describe("Standalone Resource Permits", Serial, Label("standalone", "resource-permit"), func() {
	BeforeEach(expectCapsuleAPIsAbsent)

	It("executes permits with namespace isolation and protected lifecycle", func() {
		ctx := context.Background()
		namespace := createResourcePermitTestNamespace(ctx)
		other := createResourcePermitTestNamespace(ctx)
		executor := namespace.Name + "-runner"
		grantResourcePermitServiceAccount(namespace.Name, executor, namespace.Name, []string{"get", "list", "watch", "create", "update", "patch", "delete"})
		template := &permitsv1beta2.GlobalResourcePermitTemplate{
			Name: namespace.Name,
			Spec: permitsv1beta2.GlobalResourcePermitTemplateSpec{
				NamespaceSelectors: []selectors.NamespaceSelector{{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace.Name}}}},
				Impersonation:      &meta.NamespacedRFC1123ObjectReferenceWithNamespace{Name: meta.RFC1123Name(executor), Namespace: meta.RFC1123SubdomainName(namespace.Name)},
				Approvals:          resourcepermit.ApprovalSpec{Auto: true},
				Resources: []apiruntime.ResourceTemplate{{Template: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: standalone-target
data:
  value: managed
`}},
			},
		}
		Expect(k8sClient.Create(ctx, template)).To(Succeed())
		DeferCleanup(func() { EventuallyDeletion(template) })
		expectGlobalResourcePermitTemplateNamespaces(ctx, template.Name, namespace.Name)

		By("rejecting a request in a namespace outside the template selection")
		excluded := newImpersonatedResourcePermit(other.Name, "excluded", template.Name)
		err := k8sClient.Create(ctx, excluded)
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("not available in namespace")))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(excluded), excluded))).To(BeTrue())

		By("applying the resource with the selected executor")
		request := newImpersonatedResourcePermit(namespace.Name, "standalone", template.Name)
		Expect(k8sClient.Create(ctx, request)).To(Succeed())
		DeferCleanup(func() { expireResourcePermitForCleanup(ctx, request); EventuallyDeletion(request) })
		target := &corev1.ConfigMap{Name: "standalone-target", Namespace: namespace.Name}
		Eventually(func(g Gomega) {
			current := &permitsv1beta2.ResourcePermit{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(request), current)).To(Succeed())
			g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseActive))
			expectResourcePermitServiceAccount(g, current, namespace.Name, executor)
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(target), target)).To(Succeed())
			g.Expect(target.Data).To(HaveKeyWithValue("value", "managed"))
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

		By("keeping unrelated writes and executor deletion forbidden")
		before := target.DeepCopy()
		target.Data["value"] = "unauthorized"
		err = k8sClient.Patch(ctx, target, client.MergeFrom(before))
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("ServiceAccount")))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(target), target)).To(Succeed())
		Expect(target.Data).To(HaveKeyWithValue("value", "managed"))
		err = k8sClient.Delete(ctx, &corev1.ServiceAccount{Name: executor, Namespace: namespace.Name})
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("used by unexpired ResourcePermit")))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{Name: target.Name, Namespace: other.Name}, &corev1.ConfigMap{}))).To(BeTrue())

		By("cleaning up resources and releasing the executor after expiration")
		expireActiveResourcePermit(ctx, request)
		expectResourcePermitAndConfigMapDeleted(ctx, request, target)
		Expect(k8sClient.Delete(ctx, &corev1.ServiceAccount{Name: executor, Namespace: namespace.Name})).To(Succeed())
	})

	It("fails safely when the explicit executor lacks workload permissions", func() {
		ctx := context.Background()
		namespace := createResourcePermitTestNamespace(ctx)
		executor := &corev1.ServiceAccount{Name: "unprivileged-runner", Namespace: namespace.Name}
		Expect(k8sClient.Create(ctx, executor)).To(Succeed())
		template := &permitsv1beta2.GlobalResourcePermitTemplate{Name: namespace.Name, Spec: permitsv1beta2.GlobalResourcePermitTemplateSpec{
			Impersonation: resourcePermitServiceAccountReference(namespace.Name, executor.Name),
			Approvals:     resourcepermit.ApprovalSpec{Auto: true},
			Resources: []apiruntime.ResourceTemplate{{Template: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: forbidden-target
`}},
		}}
		Expect(k8sClient.Create(ctx, template)).To(Succeed())
		DeferCleanup(func() { EventuallyDeletion(template) })
		request := newImpersonatedResourcePermit(namespace.Name, "no-executor-permissions", template.Name)
		Expect(k8sClient.Create(ctx, request)).To(Succeed())
		DeferCleanup(func() { expireResourcePermitForCleanup(ctx, request); EventuallyDeletion(request) })
		Eventually(func(g Gomega) {
			current := &permitsv1beta2.ResourcePermit{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(request), current)).To(Succeed())
			g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseFailed))
			g.Expect(current.Status.Failure).NotTo(BeNil())
			g.Expect(current.Status.Failure.Message).To(ContainSubstring("forbidden"))
			expectResourcePermitServiceAccount(g, current, namespace.Name, executor.Name)
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{Name: "forbidden-target", Namespace: namespace.Name}, &corev1.ConfigMap{}))).To(BeTrue())
	})
})

// expectCapsuleAPIsAbsent guards standalone specs against running in a cluster
// with Capsule installed, where they would not prove standalone behavior.
func expectCapsuleAPIsAbsent() {
	By("verifying that no Capsule APIs are installed")
	err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "default"}, &capsulev1beta2.CapsuleConfiguration{})
	Expect(k8smeta.IsNoMatchError(err)).To(BeTrue(), "CapsuleConfiguration must not be installed: %v", err)
	err = k8sClient.List(context.Background(), &capsulev1beta2.TenantList{})
	Expect(k8smeta.IsNoMatchError(err)).To(BeTrue(), "Tenant must not be installed: %v", err)
}
