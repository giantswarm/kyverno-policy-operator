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
	"fmt"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/giantswarm/kyverno-policy-operator/internal/controller"
)

var _ = Describe("PolicyManifest Controller", func() {
	var (
		ctx                    context.Context
		kyvernoClusterPolicy   kyvernov1.ClusterPolicy
		gsPolicyManifest       policyAPI.PolicyManifest
		r                      *controller.PolicyManifestReconciler
		policyCache            map[string]kyvernov1.ClusterPolicy
		kyvernoPolicyException kyvernov2beta1.PolicyException
	)

	BeforeEach(func() {
		// initialize the shared PolicyCache
		policyCache = make(map[string]kyvernov1.ClusterPolicy)

		// Initialize the Policy Manifest Reconciler
		r = &controller.PolicyManifestReconciler{
			Client:               k8sClient,
			Scheme:               scheme.Scheme,
			Log:                  logger,
			DestinationNamespace: "default",
			Background:           false,
			PolicyCache:          policyCache,
			MaxJitterPercent:     maxJitterPercent,
		}

		logger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
		ctx = log.IntoContext(context.Background(), logger)

		// Define the Kyverno Cluster Policy
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
						Validation: kyvernov1.Validation{
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

		// Define the Giant Swarm Policy Manifest
		gsPolicyManifest = policyAPI.PolicyManifest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "disallow-privileged-containers",
				Namespace: "default",
			},
			Spec: policyAPI.PolicyManifestSpec{
				Mode: "enforce",
				Args: []string{"--test-arg-1"},
				Exceptions: []policyAPI.Target{
					{
						Namespaces: []string{"default"},
						Names:      []string{"test-app-1"},
						Kind:       "Deployment",
					},
				},
				AutomatedExceptions: []policyAPI.Target{
					{
						Namespaces: []string{"default"},
						Names:      []string{"test-app-2"},
						Kind:       "Pod",
					},
				},
			},
		}

		// Define the Cluster Policy Reconciler.
		clusterPolicyReconciler := &controller.ClusterPolicyReconciler{
			Client:           k8sClient,
			Scheme:           scheme.Scheme,
			Log:              logger,
			PolicyCache:      policyCache,
			MaxJitterPercent: maxJitterPercent,
		}
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: kyvernoClusterPolicy.Name,
			},
		}

		// Create the Kyverno Cluster Policy and the Giant Swarm Policy Manifest in the cluster
		Expect(k8sClient.Create(ctx, &kyvernoClusterPolicy)).Should(Succeed())
		Expect(k8sClient.Create(ctx, &gsPolicyManifest)).Should(Succeed())

		// Initialize the Cluster Policy Reconciler to populate the PolicyCache. Otherwise, the policy manifest reconciliation will fail.
		_, err := clusterPolicyReconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

	})

	AfterEach(func() {
		// Clean up the Kyverno Cluster Policy and the Giant Swarm Policy Manifest
		Expect(k8sClient.Delete(ctx, &kyvernoClusterPolicy)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, &gsPolicyManifest)).Should(Succeed())
	})

	Context("When successfully reconciling a PolicyManifest", func() {
		It("should successfully create a Kyverno Policy Exception", func() {
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      gsPolicyManifest.Name,
					Namespace: "default",
				},
			}

			Expect(policyCache).NotTo(BeEmpty())

			// Test for a successful reconciliation
			result, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			// Fetch the Kyverno Policy Exception
			req = ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      fmt.Sprintf("gs-kpo-%s-exceptions", gsPolicyManifest.Name),
					Namespace: "default",
				},
			}
			err = r.Get(ctx, req.NamespacedName, &kyvernoPolicyException)
			Expect(err).NotTo(HaveOccurred())

			// Compare the Kyverno Policy Exception with the expected results
			Expect(kyvernoPolicyException.Name).To(Equal(fmt.Sprintf("gs-kpo-%s-exceptions", gsPolicyManifest.Name)))
			Expect(kyvernoPolicyException.Namespace).To(Equal("default"))
			Expect(kyvernoPolicyException.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "kyverno-policy-operator"))
			//Expect(kyvernoPolicyException.Spec.Match.Any[0].Kinds).To(ConsistOf("Deployment", "ReplicaSet", "Pod"))
			//Expect(kyvernoPolicyException.Spec.Match.Any[1].Kinds).To(ConsistOf("Pod"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].PolicyName).To(Equal("disallow-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].RuleNames[0]).To(Equal("restrict-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Names[0]).To(Equal("test-app-1*"))
			//Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Names[1]).To(Equal("test-app-2*"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Namespaces[0]).To(Equal("default"))
		})
	})
})
