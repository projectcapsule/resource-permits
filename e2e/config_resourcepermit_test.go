// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
	apimeta "github.com/projectcapsule/capsule/pkg/api/meta"
	capsulerbac "github.com/projectcapsule/capsule/pkg/api/rbac"
	apiruntime "github.com/projectcapsule/capsule/pkg/api/runtime"
	tpl "github.com/projectcapsule/capsule/pkg/template"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8smeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	permitmeta "github.com/projectcapsule/resource-permits/pkg/api/meta"
	"github.com/projectcapsule/resource-permits/pkg/api/resourcepermit"
)

const (
	resourcePermitImpersonationTemplateName = "e2e-resourcepermit-impersonation"
	resourcePermitImpersonationTargetName   = "e2e-resourcepermit-impersonation-target"
	resourcePermitImpersonationContextName  = "e2e-resourcepermit-impersonation-context"
	resourcePermitTemplateServiceAccount    = "e2e-resourcepermit-template-runner"
	resourcePermitLocalServiceAccount       = "e2e-resourcepermit-local-runner"
	resourcePermitReadOnlyServiceAccount    = "e2e-resourcepermit-readonly-runner"
	resourcePermitRetryRequester            = "e2e-resourcepermit-retry-requester"
	resourcePermitRequesterClusterRole      = "resource-permits-requester"
)

var _ = Describe(
	"ResourcePermit impersonation configuration",
	Ordered,
	Serial,
	Label("resource-permit", "config", "impersonation"),
	func() { describeGlobalTemplateImpersonation(true) },
)

var _ = Describe(
	"Standalone ResourcePermit impersonation configuration",
	Ordered,
	Serial,
	Label("standalone", "resource-permit", "impersonation"),
	func() {
		BeforeEach(expectCapsuleAPIsAbsent)
		describeGlobalTemplateImpersonation(false)
	},
)

// describeGlobalTemplateImpersonation registers the GlobalResourcePermitTemplate
// impersonation specs. Specs that need a Capsule Tenant are registered only when
// withTenant is set; the remaining specs use plain namespaces and explicit
// ServiceAccounts, so they also run without Capsule.
func describeGlobalTemplateImpersonation(withTenant bool) {
	var (
		ctx                     context.Context
		brt                     *permitsv1beta2.GlobalResourcePermitTemplate
		namespace               *corev1.Namespace
		serviceAccountNamespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		// Delete target resources before the execution identities they reference.
		serviceAccountNamespace = createResourcePermitTestNamespace(ctx).Name
		namespace = createResourcePermitTestNamespace(ctx)
		brt = &permitsv1beta2.GlobalResourcePermitTemplate{
			Name: resourcePermitImpersonationTemplateName,
			Spec: permitsv1beta2.GlobalResourcePermitTemplateSpec{
				Impersonation: resourcePermitServiceAccountReference(ControllerNamespace, ControllerServiceAccount),
				Approvals:     resourcepermit.ApprovalSpec{Auto: true},
				Resources: []apiruntime.ResourceTemplate{{Template: `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e-resourcepermit-impersonation-target
data:
  key: value
`}},
			},
		}
	})

	JustBeforeEach(func() {
		EventuallyCreation(func() error {
			brt.ResourceVersion = ""

			return k8sClient.Create(ctx, brt)
		}).Should(Succeed())
	})

	JustAfterEach(func() {
		EventuallyDeletion(brt)
	})

	if withTenant {
		Context("with an explicit template ServiceAccount", func() {
			BeforeEach(func() {
				tnt := &capsulev1beta2.Tenant{
					Name: namespace.Name + "-impersonation", Labels: map[string]string{"env": "e2e"},
					Spec: capsulev1beta2.TenantSpec{Owners: capsulerbac.OwnerListSpec{{
						Name: namespace.Name + "-owner", Kind: capsulerbac.UserOwner,
					}}},
				}
				EventuallyCreation(func() error { return k8sClient.Create(ctx, tnt) }).Should(Succeed())
				DeferCleanup(func() { EventuallyDeletion(tnt) })
				TenantReady(tnt, metav1.ConditionTrue, defaultTimeoutInterval)
				namespace = NewNamespace(tnt.Name, map[string]string{apimeta.TenantLabel: tnt.Name})
				NamespaceCreation(namespace, tnt.Spec.Owners[0].UserSpec, defaultTimeoutInterval).Should(Succeed())
				NamespaceIsPartOfTenant(tnt, namespace).Should(Succeed())
				TenantNamespaceReady(tnt, namespace, 1)

				brt.Spec.Impersonation = resourcePermitServiceAccountReference(
					serviceAccountNamespace,
					resourcePermitTemplateServiceAccount,
				)
				brt.Spec.Context = &tpl.TemplateContext{Resources: []*tpl.TemplateResourceReference{{
					APIVersion: "v1", Kind: "ConfigMap",
					Name:  resourcePermitImpersonationContextName,
					Index: "settings",
				}}}
				brt.Spec.Resources = []apiruntime.ResourceTemplate{{Template: `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e-resourcepermit-impersonation-target
data:
  loaded: {{ (index $.context.resources.settings 0).data.value }}
`}}

				grantResourcePermitServiceAccount(
					serviceAccountNamespace,
					resourcePermitTemplateServiceAccount,
					namespace.Name,
					[]string{"get", "list", "watch", "create", "update", "patch", "delete"},
				)

				source := &corev1.ConfigMap{
					Name: resourcePermitImpersonationContextName, Namespace: namespace.Name,
					Data: map[string]string{"value": "loaded-by-template-service-account"},
				}
				EventuallyCreation(func() error { return k8sClient.Create(ctx, source) }).Should(Succeed())
				DeferCleanup(func() { EventuallyDeletion(source) })
			})

			It("uses the identity for context, apply, protected updates, and deletion", func() {
				br := newImpersonatedResourcePermit(namespace.Name, "e2e-resourcepermit-impersonated", brt.Name)
				DeferCleanup(func() {
					expireResourcePermitForCleanup(ctx, br)
					EventuallyDeletion(br)
				})
				EventuallyCreation(func() error { return k8sClient.Create(ctx, br) }).Should(Succeed())

				expectedUsername := serviceAccountUsername(
					serviceAccountNamespace,
					resourcePermitTemplateServiceAccount,
				)
				cm := resourcePermitManagedConfigMap(br.Namespace)
				Eventually(func(g Gomega) {
					current := &permitsv1beta2.ResourcePermit{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
					g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseActive))
					expectResourcePermitServiceAccount(
						g,
						current,
						serviceAccountNamespace,
						resourcePermitTemplateServiceAccount,
					)

					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
					g.Expect(cm.Data).To(HaveKeyWithValue("loaded", "loaded-by-template-service-account"))
					g.Expect(cm.Annotations).To(HaveKeyWithValue(
						permitmeta.ResourcePermitServiceAccountAnnotation,
						expectedUsername,
					))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				templateClient := impersonationClient(
					expectedUsername,
					serviceAccountGroups(serviceAccountNamespace),
				)
				By("advancing the target revision before the protected data update")
				Eventually(func(g Gomega) {
					current := &corev1.ConfigMap{}
					g.Expect(templateClient.Get(ctx, client.ObjectKeyFromObject(cm), current)).To(Succeed())
					current.Annotations["e2e.projectcapsule.dev/concurrent-update"] = "preserved"
					g.Expect(templateClient.Update(ctx, current)).To(Succeed())
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				By("updating the protected target with a fresh revision on each attempt")
				Eventually(func(g Gomega) {
					current := &corev1.ConfigMap{}
					g.Expect(templateClient.Get(ctx, client.ObjectKeyFromObject(cm), current)).To(Succeed())
					current.Data["updated"] = "by-template-service-account"
					g.Expect(templateClient.Update(ctx, current)).To(Succeed())
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
				Eventually(func(g Gomega) {
					current := &corev1.ConfigMap{}
					g.Expect(templateClient.Get(ctx, client.ObjectKeyFromObject(cm), current)).To(Succeed())
					g.Expect(current.Data).To(HaveKeyWithValue("updated", "by-template-service-account"))
					g.Expect(current.Data).To(HaveKeyWithValue("loaded", "loaded-by-template-service-account"))
					g.Expect(current.Annotations).To(HaveKeyWithValue("e2e.projectcapsule.dev/concurrent-update", "preserved"))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				By("protecting the template ServiceAccount copied to ResourcePermit status")
				executionServiceAccount := &corev1.ServiceAccount{
					Name:      resourcePermitTemplateServiceAccount,
					Namespace: serviceAccountNamespace,
				}
				Eventually(func(g Gomega) {
					err := k8sClient.Delete(ctx, executionServiceAccount, client.DryRunAll)
					g.Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected protection of the active execution identity: %v", err)
					g.Expect(err).To(MatchError(ContainSubstring("used by unexpired ResourcePermit " + br.Namespace + "/" + br.Name)))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				expireActiveResourcePermit(ctx, br)
				expectResourcePermitAndConfigMapDeleted(ctx, br, cm)

				By("allowing deletion after the referencing ResourcePermit has expired")
				Eventually(func() error {
					return k8sClient.Delete(ctx, executionServiceAccount)
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(executionServiceAccount), executionServiceAccount)

					return apierrors.IsNotFound(err)
				}, defaultTimeoutInterval, defaultPollInterval).Should(BeTrue())
			})
		})
	}

	Context("without sufficient target permissions", func() {
		var requesterClient client.Client

		BeforeEach(func() {
			brt.Spec.Impersonation = resourcePermitServiceAccountReference(
				serviceAccountNamespace,
				resourcePermitReadOnlyServiceAccount,
			)
			grantResourcePermitServiceAccount(
				serviceAccountNamespace,
				resourcePermitReadOnlyServiceAccount,
				namespace.Name,
				[]string{"get", "list", "watch"},
			)
			grantResourcePermitNamespaceAdmin(ctx, namespace.Name, resourcePermitRetryRequester)
			grantResourcePermitRequesterRole(ctx, namespace.Name, resourcePermitRetryRequester)
			requesterClient = impersonationClient(
				resourcePermitRetryRequester,
				[]string{"system:authenticated"},
			)
		})

		It("fails preflight and lets the requester retry after permissions are fixed", func() {
			br := newImpersonatedResourcePermit(namespace.Name, "e2e-resourcepermit-impersonation-forbidden", brt.Name)
			DeferCleanup(func() {
				expireResourcePermitForCleanup(ctx, br)
				EventuallyDeletion(br)
			})
			EventuallyCreation(func() error { return requesterClient.Create(ctx, br) }).Should(Succeed())

			Eventually(func(g Gomega) {
				current := &permitsv1beta2.ResourcePermit{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
				expectResourcePermitServiceAccount(
					g,
					current,
					serviceAccountNamespace,
					resourcePermitReadOnlyServiceAccount,
				)

				ready := k8smeta.FindStatusCondition(current.Status.Conditions, apimeta.ReadyCondition)
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Reason).To(Equal("ResourceDryRunFailed"))
				g.Expect(ready.Message).To(ContainSubstring("forbidden"))
				g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseFailed))
				g.Expect(current.Status.Failure).NotTo(BeNil())
				g.Expect(current.Status.Failure.Stage).To(Equal(permitsv1beta2.ResourcePermitFailureStagePreflight))
				g.Expect(current.Status.Failure.RetryPhase).To(Equal(permitsv1beta2.ResourcePermitPhaseApproved))
				g.Expect(current.Status.ProcessedItems).To(BeEmpty())
			}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

			cm := resourcePermitManagedConfigMap(br.Namespace)
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			By("granting the missing permissions and retrying as the requester")
			grantResourcePermitServiceAccount(
				serviceAccountNamespace,
				resourcePermitReadOnlyServiceAccount,
				namespace.Name,
				[]string{"get", "list", "watch", "create", "update", "patch", "delete"},
			)
			patchResourcePermitPhaseAs(
				ctx,
				requesterClient,
				br,
				permitsv1beta2.ResourcePermitPhaseRetrying,
			)

			Eventually(func(g Gomega) {
				current := &permitsv1beta2.ResourcePermit{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseActive))
				g.Expect(current.Status.Failure).To(BeNil())
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
			}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

			By("letting the requester expire the recovered request")
			patchResourcePermitPhaseAs(
				ctx,
				requesterClient,
				br,
				permitsv1beta2.ResourcePermitPhaseExpired,
			)
			expectResourcePermitAndConfigMapDeleted(ctx, br, cm)
		})

		Context("when target permissions change after preflight", func() {
			BeforeEach(func() {
				// Hold activation until write permissions have been revoked.
				brt.Spec.Approvals = resourcepermit.ApprovalSpec{
					Approvers: capsulerbac.UserListSpec{{
						Kind: capsulerbac.UserOwner,
						Name: resourcePermitRetryRequester,
					}},
				}
				grantResourcePermitServiceAccount(
					serviceAccountNamespace,
					resourcePermitReadOnlyServiceAccount,
					namespace.Name,
					[]string{"get", "list", "watch", "create", "update", "patch", "delete"},
				)
			})

			It("enters Failed when write permissions are revoked and recovers on retry", func() {
				br := newImpersonatedResourcePermit(namespace.Name, "e2e-resourcepermit-activation-retry", brt.Name)
				DeferCleanup(func() {
					expireResourcePermitForCleanup(ctx, br)
					EventuallyDeletion(br)
				})
				EventuallyCreation(func() error { return requesterClient.Create(ctx, br) }).Should(Succeed())

				Eventually(func(g Gomega) {
					current := &permitsv1beta2.ResourcePermit{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
					g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseRequested))
					ready := k8smeta.FindStatusCondition(current.Status.Conditions, apimeta.ReadyCondition)
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				By("protecting the resolved ServiceAccount after successful preflight")
				serviceAccount := &corev1.ServiceAccount{
					Name:      resourcePermitReadOnlyServiceAccount,
					Namespace: serviceAccountNamespace,
				}
				Eventually(func(g Gomega) {
					err := k8sClient.Delete(ctx, serviceAccount, client.DryRunAll)
					g.Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected admission denial, got: %v", err)
					g.Expect(err).To(MatchError(ContainSubstring(
						"used by unexpired ResourcePermit " + br.Namespace + "/" + br.Name,
					)))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				setConfigMapWriteAccess := func(allowed bool) {
					verbs := []string{"get", "list", "watch"}
					if allowed {
						verbs = append(verbs, "create", "update", "patch", "delete")
					}
					bindServiceAccountToNamespacedResource(
						serviceAccountNamespace,
						resourcePermitReadOnlyServiceAccount,
						namespace.Name,
						[]string{"configmaps"},
						verbs,
					)
					// Wait for RBAC propagation before approval or retry.
					Eventually(func(g Gomega) {
						for _, verb := range []string{"create", "patch"} {
							review := &authorizationv1.SubjectAccessReview{
								Spec: authorizationv1.SubjectAccessReviewSpec{
									User:   serviceAccountUsername(serviceAccountNamespace, resourcePermitReadOnlyServiceAccount),
									Groups: serviceAccountGroups(serviceAccountNamespace),
									ResourceAttributes: &authorizationv1.ResourceAttributes{
										Namespace: namespace.Name,
										Verb:      verb,
										Resource:  "configmaps",
										Name:      resourcePermitImpersonationTargetName,
									},
								},
							}
							g.Expect(k8sClient.Create(ctx, review)).To(Succeed())
							g.Expect(review.Status.EvaluationError).To(BeEmpty())
							g.Expect(review.Status.Allowed).To(Equal(allowed), "%s ConfigMap permission: %+v", verb, review.Status)
						}
					}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
				}
				// Restore access before permit cleanup, including when an assertion fails.
				DeferCleanup(setConfigMapWriteAccess, true)
				By("revoking write permissions and approving the stored snapshot")
				setConfigMapWriteAccess(false)
				patchResourcePermitPhaseAs(ctx, requesterClient, br, permitsv1beta2.ResourcePermitPhaseApproved)

				Eventually(func(g Gomega) {
					current := &permitsv1beta2.ResourcePermit{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
					g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseFailed))
					g.Expect(current.Status.Failure).NotTo(BeNil())
					g.Expect(current.Status.Failure.Stage).To(Equal(permitsv1beta2.ResourcePermitFailureStageActivation))
					g.Expect(current.Status.Failure.RetryPhase).To(Equal(permitsv1beta2.ResourcePermitPhaseApproved))
					ready := k8smeta.FindStatusCondition(current.Status.Conditions, apimeta.ReadyCondition)
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(ready.Reason).To(Equal("ResourceApplyFailed"))
					g.Expect(ready.Message).To(ContainSubstring("forbidden"))
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				cm := resourcePermitManagedConfigMap(br.Namespace)
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)
				Expect(apierrors.IsNotFound(err)).To(BeTrue(), "failed activation must not create the target: %v", err)

				By("restoring write permissions and retrying the stored approved snapshot")
				setConfigMapWriteAccess(true)
				patchResourcePermitPhaseAs(
					ctx,
					requesterClient,
					br,
					permitsv1beta2.ResourcePermitPhaseRetrying,
				)

				Eventually(func(g Gomega) {
					current := &permitsv1beta2.ResourcePermit{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
					g.Expect(current.Status.Phase).To(Equal(permitsv1beta2.ResourcePermitPhaseActive),
						"ResourcePermit retry did not activate: failure=%+v, conditions=%+v, resources=%+v",
						current.Status.Failure, current.Status.Conditions, current.Status.ProcessedItems)
					g.Expect(current.Status.Failure).To(BeNil())
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
				}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

				patchResourcePermitPhaseAs(
					ctx,
					requesterClient,
					br,
					permitsv1beta2.ResourcePermitPhaseExpired,
				)
				expectResourcePermitAndConfigMapDeleted(ctx, br, cm)
			})
		})
	})
}

var _ = Describe(
	"Namespaced ResourcePermitTemplate impersonation configuration",
	Ordered,
	Serial,
	Label("resource-permit", "config", "impersonation"),
	describeNamespacedTemplateImpersonation,
)

var _ = Describe(
	"Standalone namespaced ResourcePermitTemplate impersonation configuration",
	Ordered,
	Serial,
	Label("standalone", "resource-permit", "impersonation"),
	func() {
		BeforeEach(expectCapsuleAPIsAbsent)
		describeNamespacedTemplateImpersonation()
	},
)

// describeNamespacedTemplateImpersonation registers the namespaced
// ResourcePermitTemplate impersonation specs. They use plain namespaces only.
func describeNamespacedTemplateImpersonation() {
	var (
		ctx       context.Context
		brt       *permitsv1beta2.ResourcePermitTemplate
		namespace *corev1.Namespace
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = createResourcePermitTestNamespace(ctx)

		grantResourcePermitServiceAccount(
			namespace.Name,
			resourcePermitLocalServiceAccount,
			namespace.Name,
			[]string{"get", "list", "watch", "create", "update", "patch", "delete"},
		)

		brt = &permitsv1beta2.ResourcePermitTemplate{
			Name: "e2e-resourcepermit-local-template", Namespace: namespace.Name,
			Spec: permitsv1beta2.ResourcePermitTemplateSpec{
				Impersonation: &apimeta.LocalRFC1123ObjectReference{Name: resourcePermitLocalServiceAccount},
				Approvals:     resourcepermit.ApprovalSpec{Auto: true},
				Resources: []apiruntime.ResourceTemplate{{Template: `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e-resourcepermit-impersonation-target
data:
  source: namespaced-template
`}},
			},
		}
		EventuallyCreation(func() error { return k8sClient.Create(ctx, brt) }).Should(Succeed())
		DeferCleanup(func() { EventuallyDeletion(brt) })
	})

	It("uses the explicit namespace-local ServiceAccount and records template provenance", func() {
		br := &permitsv1beta2.ResourcePermit{
			Name: "e2e-resourcepermit-local", Namespace: namespace.Name,
			Spec: permitsv1beta2.ResourcePermitSpec{Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: permitsv1beta2.ResourcePermitTemplateKind,
				Name: brt.Name,
			}},
		}
		DeferCleanup(func() {
			expireResourcePermitForCleanup(ctx, br)
			EventuallyDeletion(br)
		})
		EventuallyCreation(func() error { return k8sClient.Create(ctx, br) }).Should(Succeed())

		cm := resourcePermitManagedConfigMap(br.Namespace)
		Eventually(func(g Gomega) {
			current := &permitsv1beta2.ResourcePermit{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(br), current)).To(Succeed())
			expectResourcePermitServiceAccount(
				g,
				current,
				namespace.Name,
				resourcePermitLocalServiceAccount,
			)
			g.Expect(current.Status.Request.Template).NotTo(BeNil())
			g.Expect(current.Status.Request.Template.Kind).To(Equal(permitsv1beta2.ResourcePermitTemplateKind))
			g.Expect(current.Status.Request.Template.Name).To(Equal(brt.Name))
			g.Expect(current.Status.Request.Template.ResourceVersion).NotTo(BeEmpty())

			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
			g.Expect(cm.Data).To(HaveKeyWithValue("source", "namespaced-template"))
			g.Expect(cm.Annotations).To(HaveKeyWithValue(
				permitmeta.ResourcePermitServiceAccountAnnotation,
				serviceAccountUsername(namespace.Name, resourcePermitLocalServiceAccount),
			))
		}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())

		expireActiveResourcePermit(ctx, br)
		expectResourcePermitAndConfigMapDeleted(ctx, br, cm)
	})

	It("does not resolve a namespaced template from another namespace", func() {
		otherNamespace := createResourcePermitTestNamespace(ctx)

		br := &permitsv1beta2.ResourcePermit{
			Name: "e2e-resourcepermit-cross-namespace", Namespace: otherNamespace.Name,
			Spec: permitsv1beta2.ResourcePermitSpec{Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: permitsv1beta2.ResourcePermitTemplateKind,
				Name: brt.Name,
			}},
		}

		err := k8sClient.Create(ctx, br)
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("template e2e-resourcepermit-local-template not found")))
	})
}

func resourcePermitServiceAccountReference(
	namespace,
	name string,
) *apimeta.NamespacedRFC1123ObjectReferenceWithNamespace {
	return &apimeta.NamespacedRFC1123ObjectReferenceWithNamespace{
		Name:      apimeta.RFC1123Name(name),
		Namespace: apimeta.RFC1123SubdomainName(namespace),
	}
}

func newImpersonatedResourcePermit(namespace, name, template string) *permitsv1beta2.ResourcePermit {
	return &permitsv1beta2.ResourcePermit{
		Name: name, Namespace: namespace,
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: globalResourcePermitTemplateReference(template),
		},
	}
}

func resourcePermitManagedConfigMap(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		Name:      resourcePermitImpersonationTargetName,
		Namespace: namespace,
	}
}

// grantResourcePermitRequesterRole binds the chart requester ClusterRole in
// namespace. The chart aggregates it into admin only when configured, as in
// the Capsule fixture; the standalone install relies on an explicit binding.
func grantResourcePermitRequesterRole(ctx context.Context, namespace, username string) {
	roleBinding := &rbacv1.RoleBinding{
		GenerateName: "e2e-resourcepermit-requester-",
		Namespace:    namespace,
		Subjects: []rbacv1.Subject{{
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.UserKind,
			Name:     username,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     resourcePermitRequesterClusterRole,
		},
	}

	Expect(k8sClient.Create(ctx, roleBinding)).To(Succeed())
	DeferCleanup(func() {
		EventuallyDeletion(roleBinding)
	})
}

func grantResourcePermitServiceAccount(namespace, name, targetNamespace string, configMapVerbs []string) {
	ensureServiceAccount(namespace, name)
	bindServiceAccountToNamespacedResource(
		namespace,
		name,
		targetNamespace,
		[]string{"configmaps"},
		configMapVerbs,
	)
	bindServiceAccountToClusterResources(
		namespace,
		name,
		name+"-namespaces",
		name+"-namespaces-binding",
		[]rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     []string{"get"},
		}},
	)
}

func expectResourcePermitServiceAccount(
	g Gomega,
	request *permitsv1beta2.ResourcePermit,
	namespace,
	name string,
) {
	g.Expect(request.Status.Request).NotTo(BeNil(), "ResourcePermit status is not initialized: %+v", request.Status)
	g.Expect(request.Status.Request.Impersonation).ToNot(BeNil())
	g.Expect(request.Status.Request.Impersonation.Name.String()).To(Equal(name))
	g.Expect(request.Status.Request.Impersonation.Namespace.String()).To(Equal(namespace))
}

func expectResourcePermitAndConfigMapDeleted(
	ctx context.Context,
	request *permitsv1beta2.ResourcePermit,
	configMap *corev1.ConfigMap,
) {
	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

		current := &permitsv1beta2.ResourcePermit{}
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(request), current)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
}

func patchResourcePermitPhaseAs(
	ctx context.Context,
	actor client.Client,
	request *permitsv1beta2.ResourcePermit,
	phase permitsv1beta2.ResourcePermitPhase,
) {
	Eventually(func() error {
		current := &permitsv1beta2.ResourcePermit{}
		if err := actor.Get(ctx, client.ObjectKeyFromObject(request), current); err != nil {
			return err
		}

		before := current.DeepCopy()
		current.Status.Phase = phase

		return actor.Status().Patch(
			ctx,
			current,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		)
	}, defaultTimeoutInterval, defaultPollInterval).Should(Succeed())
}
