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
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var _ = Describe("Converting GSPolicyException to Kyverno Policy Exception", func() {
	var (
		ctx               context.Context
		r                 *PolicyExceptionReconciler
		gsPolicyException policyAPI.PolicyException
	)

	BeforeEach(func() {
		logger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
		ctx = log.IntoContext(context.Background(), logger)

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
				Policies: []string{"restrict-privileged-containers", "restrict-host-network"},
			},
		}

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
			Expect(result.Requeue).To(BeFalse())

			// Then we test if the Kyverno Policy Exception was created
			var kyvernoPolicyException kyvernov2beta1.PolicyException
			err = r.Get(ctx, req.NamespacedName, &kyvernoPolicyException)
			Expect(err).NotTo(HaveOccurred())

			// Now we compare the Kyverno Policy Exception with the expected results.
			Expect(kyvernoPolicyException.Name).To(Equal("test-policyexception"))
			Expect(kyvernoPolicyException.Namespace).To(Equal("default"))
			Expect(kyvernoPolicyException.Spec.Match.GetKinds()).To(ContainElement("Deployment", "ReplicaSet", "Pod"))

		})
	})
})
