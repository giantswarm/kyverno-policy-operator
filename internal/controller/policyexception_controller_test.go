/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller_test

import (
	"context"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/giantswarm/kyverno-policy-operator/internal/controller"
)

var _ = Describe("Converting GSPolicyException to Kyverno Policy Exception", func() {
	var (
		ctx                    context.Context
		kyvernoClusterPolicy   kyvernov1.ClusterPolicy
		gsPolicyException      policyAPI.PolicyException
		r                      *controller.PolicyExceptionReconciler
		kyvernoPolicyException kyvernov2.PolicyException
		req                    ctrl.Request
	)

	BeforeEach(func() {
		// We initialize the Policy Exception Reconciler first.
		r = &controller.PolicyExceptionReconciler{
			Client:               k8sClient,
			Scheme:               scheme.Scheme,
			Log:                  logger,
			DestinationNamespace: "default",
			Background:           false,
			MaxJitterPercent:     maxJitterPercent,
			LegacyMode:           controller.LegacyWrite,
		}

		logger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
		ctx = log.IntoContext(context.Background(), logger)

		// We then define the Kyverno Cluster Policy and the Giant Swarm Policy Exception.
		kyvernoClusterPolicy = kyvernov1.ClusterPolicy{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "kyverno.io/v1",
				Kind:       "ClusterPolicy",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "disallow-privileged-containers",
			},
			Spec: kyvernov1.Spec{
				Rules: []kyvernov1.Rule{
					{
						Name: "restrict-privileged-containers",
						MatchResources: kyvernov1.MatchResources{
							Any: []kyvernov1.ResourceFilter{
								{ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{"Deployment"}}},
							},
						},
						Validation: &kyvernov1.Validation{
							Message: "Privileged mode is disallowed. The fields spec.containers[*].securityContext.privileged and spec.initContainers[*].securityContext.privileged must be unset or set to `false`.",
							RawPattern: &apiextv1.JSON{
								Raw: []byte(`{
          							"spec": {
            							"ephemeralContainers": [
              								{
                								"securityContext": {
												  "privileged": "false"
                								}
              								}
										],
            							"initContainers": [
              								{
                								"securityContext": {
                  								  "privileged": "false"
                								}
              								}
            							],
            							"containers": [
              								{
												"securityContext": {
                  								  "privileged": "false"
                							}
              							}
            						]
								}
        					}`),
							},
						},
					},
				},
			},
		}

		gsPolicyException = policyAPI.PolicyException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-policyexception",
				Namespace: "default",
			},

			Spec: policyAPI.PolicyExceptionSpec{
				Targets: []policyAPI.Target{
					{
						Namespaces: []string{"default"},
						Names:      []string{"test-app-1"},
						Kind:       "Deployment",
					},
				},
				Policies: []string{"disallow-privileged-containers"},
			},
		}

		// We put the Kyverno Cluster Policy and the Giant Swarm Policy Exception in the cluster.
		Expect(k8sClient.Create(ctx, &kyvernoClusterPolicy)).Should(Succeed())
		Expect(k8sClient.Create(ctx, &gsPolicyException)).Should(Succeed())

		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      gsPolicyException.Name,
				Namespace: gsPolicyException.Namespace,
			},
		}
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, &kyvernoClusterPolicy)
		Expect(k8sClient.Delete(ctx, &gsPolicyException)).Should(Succeed())
		_ = k8sClient.Delete(ctx, &kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: gsPolicyException.Name, Namespace: "default"}})
		_ = k8sClient.Delete(ctx, &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: gsPolicyException.Name, Namespace: "default"}})
	})

	Context("When succesfully reconciling a GSPolicyException", func() {
		It("should successfully create a Kyverno Policy Exception", func() {
			// First we test for a successful reconciliation
			result, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// Then we test if we can fetch the Kyverno Policy Exception.
			err = r.Get(ctx, req.NamespacedName, &kyvernoPolicyException)

			Expect(err).NotTo(HaveOccurred())

			// Now we compare the Kyverno Policy Exception with the expected results.
			Expect(kyvernoPolicyException.Name).To(Equal("test-policyexception"))
			Expect(kyvernoPolicyException.Namespace).To(Equal("default"))
			Expect(kyvernoPolicyException.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "kyverno-policy-operator"))
			Expect(kyvernoPolicyException.Labels).To(HaveKeyWithValue("policy.giantswarm.io/source", "gspolex"))
			Expect(kyvernoPolicyException.Spec.Match.GetKinds()).To(ConsistOf("Deployment", "ReplicaSet", "Pod"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].PolicyName).To(Equal("disallow-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].RuleNames[0]).To(Equal("restrict-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Names[0]).To(Equal("test-app-1*"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Namespaces[0]).To(Equal("default"))
		})
	})

	Context("When the policy is not a ClusterPolicy", func() {
		It("does not write a legacy exception and does not block", func() {
			Expect(k8sClient.Delete(ctx, &kyvernoClusterPolicy)).Should(Succeed())
			result, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			err = k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When the ClusterPolicy is removed after the legacy exception exists", func() {
		It("deletes the legacy exception", func() {
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})).To(Succeed())

			Expect(k8sClient.Delete(ctx, &kyvernoClusterPolicy)).Should(Succeed())
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When legacy exceptions are switched off", func() {
		It("deletes the managed legacy exception", func() {
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			r.LegacyMode = controller.LegacyCleanup
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When the gspolex was migrated by exception-recommender", func() {
		It("does not write a legacy exception", func() {
			gsPolicyException.Annotations = map[string]string{"policy.giantswarm.io/migrated-from": "team-a/foo"}
			Expect(k8sClient.Update(ctx, &gsPolicyException)).To(Succeed())
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	newVpol := func() *policiesv1.ValidatingPolicy {
		return &policiesv1.ValidatingPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged-containers"},
			Spec: policiesv1.ValidatingPolicySpec{
				MatchConstraints: &admissionregistrationv1.MatchResources{
					ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{{
						RuleWithOperations: admissionregistrationv1.RuleWithOperations{
							Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
							Rule:       admissionregistrationv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"}},
						},
					}},
				},
				Validations: []admissionregistrationv1.Validation{{Expression: "true"}},
			},
		}
	}

	Context("CEL exceptions", func() {
		var celException policiesv1.PolicyException

		It("references the ValidatingPolicy and matches the targets", func() {
			vpol := newVpol()
			Expect(k8sClient.Create(ctx, vpol)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, vpol) })

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &celException)).To(Succeed())
			Expect(celException.Spec.PolicyRefs).To(ConsistOf(policiesv1.PolicyRef{Name: "disallow-privileged-containers", Kind: "ValidatingPolicy"}))
			Expect(celException.Spec.MatchConditions).To(HaveLen(1))
			Expect(celException.Spec.MatchConditions[0].Name).To(Equal("gspolex-targets"))
			Expect(celException.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "kyverno-policy-operator"))
			Expect(celException.Labels).To(HaveKeyWithValue("policy.giantswarm.io/source", "gspolex"))
			Expect(celException.Annotations).NotTo(HaveKey("policy.giantswarm.io/unresolved-policies"))
			Expect(celException.OwnerReferences).To(HaveLen(1))
			Expect(celException.OwnerReferences[0].Name).To(Equal(gsPolicyException.Name))
		})

		It("writes both exceptions while the policy exists in both forms", func() {
			vpol := newVpol()
			Expect(k8sClient.Create(ctx, vpol)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, vpol) })

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &kyvernov2.PolicyException{})).To(Succeed())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &celException)).To(Succeed())
		})

		It("falls back to ValidatingPolicy and records unresolved names", func() {
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &celException)).To(Succeed())
			Expect(celException.Spec.PolicyRefs).To(ConsistOf(policiesv1.PolicyRef{Name: "disallow-privileged-containers", Kind: "ValidatingPolicy"}))
			Expect(celException.Annotations).To(HaveKeyWithValue("policy.giantswarm.io/unresolved-policies", "disallow-privileged-containers"))
		})

		It("marks exceptions migrated by exception-recommender", func() {
			gsPolicyException.Annotations = map[string]string{"policy.giantswarm.io/migrated-from": "team-a/foo"}
			Expect(k8sClient.Update(ctx, &gsPolicyException)).To(Succeed())
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &celException)).To(Succeed())
			Expect(celException.Labels).To(HaveKeyWithValue("policy.giantswarm.io/source", "exception-recommender"))
			Expect(celException.Annotations).To(HaveKeyWithValue("policy.giantswarm.io/migrated-from", "team-a/foo"))
		})

		It("matches nothing when the gspolex has no targets", func() {
			gsPolicyException.Spec.Targets = []policyAPI.Target{}
			Expect(k8sClient.Update(ctx, &gsPolicyException)).To(Succeed())
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &celException)).To(Succeed())
			Expect(celException.Spec.MatchConditions[0].Expression).To(Equal("false"))
		})
	})
})
