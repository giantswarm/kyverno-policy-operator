/*

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
// +kubebuilder:docs-gen:collapse=Apache License

package controller

import (
	"context"

	"time"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"

	kyvernov2alpha1 "github.com/kyverno/kyverno/api/kyverno/v2alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	giantswarmPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
)

var _ = Describe("PolicyException controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		PolicyExceptionName      = "test-policy-exception"
		PolicyExceptionNamespace = "default"
		PolicyName               = "require-run-as-nonroot"
		PolicyRuleName           = "run-as-non-root"
		ResourceName             = "test-workload*"
		ResourceNamespace        = "default"
		ResourceKind             = "Deployment"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Describe("reconciling a Giant Swarm PolicyException policy", Ordered, func() {
		BeforeAll(func() {
			logger := zap.New(zap.WriteTo(GinkgoWriter))
			ctx = log.IntoContext(context.Background(), logger)

			// Create Giant Swarm PolicyException
			policyException := &giantswarmPolicy.PolicyException{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "policy.giantswarm.io/v1alpha1",
					Kind:       "PolicyException",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      PolicyExceptionName,
					Namespace: PolicyExceptionNamespace,
				},
				Spec: giantswarmPolicy.PolicyExceptionSpec{
					Policies: []string{PolicyName},
					Targets: []giantswarmPolicy.Target{
						{
							Namespaces: []string{ResourceNamespace},
							Names:      []string{ResourceName},
							Kind:       ResourceKind,
						},
					},
				},
			}

			// Create Kyverno ClusterPolicy
			clusterPolicy := &kyvernov1.ClusterPolicy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kyverno.io/v1",
					Kind:       "ClusterPolicy",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: PolicyName,
				},
				Spec: kyvernov1.Spec{
					Rules: []kyvernov1.Rule{
						{
							Name: PolicyRuleName,
							MatchResources: kyvernov1.MatchResources{
								All: kyvernov1.ResourceFilters{
									kyvernov1.ResourceFilter{
										ResourceDescription: kyvernov1.ResourceDescription{
											Kinds:      []string{ResourceKind},
											Namespaces: []string{ResourceNamespace},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterPolicy)).Should(Succeed())
			Expect(k8sClient.Create(ctx, policyException)).Should(Succeed())

			// Create second Kyverno ClusterPolicy
			clusterPolicy.ObjectMeta = metav1.ObjectMeta{
				Name: PolicyName + "-strict",
			}
			Expect(k8sClient.Create(ctx, clusterPolicy)).Should(Succeed())
		})

		policyExceptionLookupKey := types.NamespacedName{Name: PolicyExceptionName, Namespace: PolicyExceptionNamespace}
		kyvernoPolicyException := kyvernov2alpha1.PolicyException{}

		When("a GiantSwarm PolicyException is created", func() {
			It("must create a Kyverno PolicyException", func() {
				Eventually(func() bool {
					err := k8sClient.Get(ctx, policyExceptionLookupKey, &kyvernoPolicyException)
					if err != nil {
						return false
					}
					return true
				}, timeout, interval).Should(BeTrue())
			})
		})

		When("a policy is added to a GiantSwarm PolicyException", func() {
			JustBeforeEach(func() {
				// A Kyverno PolicyException must exist
				Eventually(func() bool {
					err := k8sClient.Get(ctx, policyExceptionLookupKey, &kyvernoPolicyException)
					if err != nil {
						return false
					}
					return true
				}, timeout, interval).Should(BeTrue())

				// Update GS PolicyException
				// Get exception
				gsPolicyException := giantswarmPolicy.PolicyException{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, policyExceptionLookupKey, &gsPolicyException)
					if err != nil {
						return false
					}
					return true
				}, timeout, interval).Should(BeTrue())

				// Update policies list
				gsPolicyException.Spec.Policies = append(gsPolicyException.Spec.Policies, PolicyName+"-strict")
				Eventually(func() bool {
					err := k8sClient.Update(ctx, &gsPolicyException)
					if err != nil {
						return false
					}
					return true
				}, timeout, interval).Should(BeTrue())
			})

			It("the Kyverno PolicyException must be updated", func() {
				kyvernoPolicyException := kyvernov2alpha1.PolicyException{}
				Eventually(func() (int, error) {
					err := k8sClient.Get(ctx, policyExceptionLookupKey, &kyvernoPolicyException)
					if err != nil {
						return -1, err
					}
					return len(kyvernoPolicyException.Spec.Exceptions), nil
				}, duration, interval).Should(Equal(2))
			})
		})
	})

})
