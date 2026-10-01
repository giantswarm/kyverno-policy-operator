package controller_test

import (
	"context"
	"time"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/giantswarm/kyverno-policy-operator/internal/controller"
)

// Runs the PolicyException controller in a manager, so the test goes through the real watches and
// fails if a predicate drops ClusterPolicy status changes.
var _ = Describe("ClusterPolicy watch", func() {
	It("refreshes the legacy exception when only the ClusterPolicy status changes", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme.Scheme, Metrics: metricsserver.Options{BindAddress: "0"}})
		Expect(err).NotTo(HaveOccurred())
		Expect((&controller.PolicyExceptionReconciler{
			Client:               mgr.GetClient(),
			Scheme:               mgr.GetScheme(),
			DestinationNamespace: "default",
			MaxJitterPercent:     maxJitterPercent,
			LegacyMode:           controller.LegacyWrite,
		}).SetupWithManager(mgr)).To(Succeed())
		mgrCtx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		clusterPolicy := &kyvernov1.ClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "watch-require-team"},
			Spec: kyvernov1.Spec{Rules: []kyvernov1.Rule{{
				Name: "require-team",
				MatchResources: kyvernov1.MatchResources{Any: kyvernov1.ResourceFilters{{
					ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{"Pod"}},
				}}},
				Validation: &kyvernov1.Validation{
					Message:    "team label required",
					RawPattern: &apiextv1.JSON{Raw: []byte(`{"metadata":{"labels":{"team":"?*"}}}`)},
				},
			}}},
		}
		gspolex := &policyAPI.PolicyException{
			ObjectMeta: metav1.ObjectMeta{Name: "watch-gspolex", Namespace: "default"},
			Spec: policyAPI.PolicyExceptionSpec{
				Policies: []string{clusterPolicy.Name},
				Targets:  []policyAPI.Target{{Kind: "Pod", Namespaces: []string{"default"}, Names: []string{"app"}}},
			},
		}
		key := types.NamespacedName{Namespace: "default", Name: gspolex.Name}
		Expect(k8sClient.Create(ctx, clusterPolicy)).To(Succeed())
		Expect(k8sClient.Create(ctx, gspolex)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, gspolex)
			_ = k8sClient.Delete(ctx, clusterPolicy)
			_ = k8sClient.Delete(ctx, &kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}})
			_ = k8sClient.Delete(ctx, &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}})
		})

		ruleNames := func() []string {
			var legacy kyvernov2.PolicyException
			if err := k8sClient.Get(ctx, key, &legacy); err != nil || len(legacy.Spec.Exceptions) != 1 {
				return nil
			}
			return legacy.Spec.Exceptions[0].RuleNames
		}
		Eventually(ruleNames, 10*time.Second).Should(ConsistOf("require-team"))

		// Kyverno writes autogen rules to the status, which does not change the generation.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterPolicy.Name}, clusterPolicy)).To(Succeed())
		generation := clusterPolicy.Generation
		ready := true
		clusterPolicy.Status.Ready = &ready
		clusterPolicy.Status.Autogen.Rules = []kyvernov1.Rule{{Name: "autogen-require-team"}}
		Expect(k8sClient.Status().Update(ctx, clusterPolicy)).To(Succeed())
		Expect(clusterPolicy.Generation).To(Equal(generation))

		Eventually(ruleNames, 10*time.Second).Should(ConsistOf("require-team", "autogen-require-team"))
	})
})
