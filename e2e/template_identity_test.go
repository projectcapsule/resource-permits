// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
)

var _ = Describe("Template execution identity schema", Serial, Label("config", "explicit-executor", "resource-permit"), func() {
	It("requires an executor on create and update in tenant namespaces", func() {
		namespace := createResourcePermitTenantNamespace(context.Background())
		verifyTemplateIdentitySchema(namespace.Name)
	})
})

var _ = Describe("Standalone template execution identity schema", Serial, Label("standalone", "resource-permit"), func() {
	It("requires an executor on create and update without Capsule", func() {
		namespace := createResourcePermitTestNamespace(context.Background())
		verifyTemplateIdentitySchema(namespace.Name)
	})
})

func verifyTemplateIdentitySchema(namespace string) {
	ctx := context.Background()
	for _, kind := range []string{"ResourcePermitTemplate", "GlobalResourcePermitTemplate"} {
		template := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": permitsv1beta2.GroupVersion.String(), "kind": kind,
			"metadata": map[string]any{"name": namespace + "-identity"},
			"spec": map[string]any{
				"resources": []any{map[string]any{"template": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n"}},
			},
		}}
		if kind == "ResourcePermitTemplate" {
			template.SetNamespace(namespace)
		}
		By("rejecting " + kind + " without an execution ServiceAccount")
		err := k8sClient.Create(ctx, template)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "CRD schema must reject the missing identity: %v", err)
		Expect(err.Error()).To(ContainSubstring("spec.impersonation"))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(template), template))).To(BeTrue())

		By("accepting " + kind + " with an explicit executor")
		identity := map[string]any{"name": "default"}
		if kind == "GlobalResourcePermitTemplate" {
			identity["namespace"] = namespace
		}
		Expect(unstructured.SetNestedMap(template.Object, identity, "spec", "impersonation")).To(Succeed())
		Expect(k8sClient.Create(ctx, template)).To(Succeed())
		DeferCleanup(func() { EventuallyDeletion(template) })

		By("rejecting removal of the executor while preserving the template")
		Eventually(func() error {
			current := &unstructured.Unstructured{}
			current.SetGroupVersionKind(template.GroupVersionKind())
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(template), current); err != nil {
				return err
			}
			unstructured.RemoveNestedField(current.Object, "spec", "impersonation")
			err := k8sClient.Update(ctx, current)
			if apierrors.IsConflict(err) {
				return err
			}
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "CRD schema must reject identity removal: %v", err)
			Expect(err.Error()).To(ContainSubstring("spec.impersonation"))
			return nil
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
		Eventually(func() error {
			current := &unstructured.Unstructured{}
			current.SetGroupVersionKind(template.GroupVersionKind())
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(template), current); err != nil {
				return err
			}
			actual, found, err := unstructured.NestedMap(current.Object, "spec", "impersonation")
			if err != nil {
				return err
			}
			if !found || actual["name"] != "default" {
				return fmt.Errorf("execution identity changed after rejected update: %v", actual)
			}
			return nil
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
	}
}
