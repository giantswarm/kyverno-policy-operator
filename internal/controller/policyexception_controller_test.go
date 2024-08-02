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

package controller

import (
	"context"

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
)

var _ = Describe("Converting GSPolicyException to Kyverno Policy Exception", func() {
	var (
		ctx                    context.Context
		kyvernoClusterPolicy   kyvernov1.ClusterPolicy
		gsPolicyException      policyAPI.PolicyException
		r                      *PolicyExceptionReconciler
		kyvernoPolicyException kyvernov2beta1.PolicyException
	)

	BeforeEach(func() {

		// We initialize the Policy Exception Reconciler first.

		r = &PolicyExceptionReconciler{
			Client:               k8sClient,
			Scheme:               scheme.Scheme,
			Log:                  logger,
			DestinationNamespace: "default",
			Background:           false,
			MaxJitterPercent:     maxJitterPercent,
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

	})

	Context("When succesfully reconciling a GSPolicyException", func() {
		It("should successfully create a Kyverno Policy Exception", func() {
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      gsPolicyException.Name,
					Namespace: gsPolicyException.Namespace,
				},
			}
			// First we test for a successful reconciliation
			result, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			// Then we test if we can fetch the Kyverno Policy Exception.
			err = r.Get(ctx, req.NamespacedName, &kyvernoPolicyException)

			Expect(err).NotTo(HaveOccurred())

			// Now we compare the Kyverno Policy Exception with the expected results.
			Expect(kyvernoPolicyException.Name).To(Equal("test-policyexception"))
			Expect(kyvernoPolicyException.Namespace).To(Equal("default"))
			Expect(kyvernoPolicyException.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "kyverno-policy-operator"))
			Expect(kyvernoPolicyException.Spec.Match.GetKinds()).To(ConsistOf("Deployment", "ReplicaSet", "Pod"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].PolicyName).To(Equal("disallow-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Exceptions[0].RuleNames[0]).To(Equal("restrict-privileged-containers"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Names[0]).To(Equal("test-app-1*"))
			Expect(kyvernoPolicyException.Spec.Match.Any[0].ResourceDescription.Namespaces[0]).To(Equal("default"))

			// We clean up the Kyverno Cluster Policy and the Giant Swarm Policy Exception.
			Expect(k8sClient.Delete(ctx, &kyvernoClusterPolicy)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, &gsPolicyException)).Should(Succeed())
		})
	})
})
