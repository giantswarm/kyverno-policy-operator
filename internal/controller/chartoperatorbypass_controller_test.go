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

	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/giantswarm/kyverno-policy-operator/internal/controller"
)

var _ = Describe("CEL chart-operator bypass", func() {
	var (
		ctx  = context.Background()
		r    *controller.ChartOperatorBypassReconciler
		vpol *policiesv1.ValidatingPolicy
		key  = types.NamespacedName{Namespace: "giantswarm", Name: controller.ChartOperatorBypassName}
	)

	BeforeEach(func() {
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "giantswarm"}})
		Expect(err == nil || apierrors.IsAlreadyExists(err)).To(BeTrue())
		r = &controller.ChartOperatorBypassReconciler{Client: k8sClient, Kinds: []string{"Namespace", "PolicyException"}, Namespace: "giantswarm"}
		vpol = &policiesv1.ValidatingPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "restrict-namespaces"},
			Spec: policiesv1.ValidatingPolicySpec{
				MatchConstraints: &admissionregistrationv1.MatchResources{ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
						Rule:       admissionregistrationv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"namespaces"}},
					},
				}}},
				Validations: []admissionregistrationv1.Validation{{Expression: "true"}},
			},
		}
		Expect(k8sClient.Create(ctx, vpol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, vpol) })
	})

	It("references every ValidatingPolicy and is removed with the last one", func() {
		_, err := r.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		var bypass policiesv1.PolicyException
		Expect(k8sClient.Get(ctx, key, &bypass)).To(Succeed())
		Expect(bypass.Spec.PolicyRefs).To(ContainElement(policiesv1.PolicyRef{Name: "restrict-namespaces", Kind: "ValidatingPolicy"}))
		Expect(bypass.Spec.MatchConditions[0].Name).To(Equal("chart-operator-sa"))
		Expect(bypass.Labels).To(HaveKeyWithValue("policy.giantswarm.io/source", "chart-operator"))

		Expect(k8sClient.Delete(ctx, vpol)).To(Succeed())

		// Other specs in this suite may create and clean up their own ValidatingPolicies. Only
		// assert removal once none are left, so a leftover elsewhere can't make this flaky.
		var remaining policiesv1.ValidatingPolicyList
		Expect(k8sClient.List(ctx, &remaining)).To(Succeed())
		for _, leftover := range remaining.Items {
			Expect(k8sClient.Delete(ctx, &leftover)).To(Succeed())
		}

		_, err = r.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, &bypass))).To(BeTrue())
	})
})
