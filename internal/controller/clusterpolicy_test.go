package controller

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// A rule that writes its kind as group/version/Kind, such as kyverno-app's
// restrict-polex-namespaces, still gets the legacy chart-operator bypass.
func TestClusterPolicyBypassForQualifiedKind(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t, kyvernov1.Install, kyvernov2.Install)
	clusterPolicy := &kyvernov1.ClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "restrict-polex-namespaces"},
		Spec: kyvernov1.Spec{Rules: []kyvernov1.Rule{{
			Name: "restrict-namespaces",
			MatchResources: kyvernov1.MatchResources{Any: kyvernov1.ResourceFilters{{
				ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{"kyverno.io/v2/PolicyException"}},
			}}},
			Validation: &kyvernov1.Validation{Message: "PolicyExceptions are only allowed in some namespaces"},
		}}},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(clusterPolicy).Build()
	r := &ClusterPolicyReconciler{
		Client:                      c,
		Scheme:                      s,
		Log:                         logr.Discard(),
		ExceptionList:               map[string]kyvernov1.ClusterPolicy{},
		ChartOperatorExceptionKinds: []string{"Namespace", "PolicyException"},
		PolicyCache:                 map[string]kyvernov1.ClusterPolicy{},
		MaxJitterPercent:            10,
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: clusterPolicy.Name}}); err != nil {
		t.Fatal(err)
	}

	var bypass kyvernov2.PolicyException
	if err := c.Get(ctx, types.NamespacedName{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}, &bypass); err != nil {
		t.Fatalf("legacy chart-operator bypass not written: %v", err)
	}
	if len(bypass.Spec.Exceptions) != 1 || bypass.Spec.Exceptions[0].PolicyName != clusterPolicy.Name {
		t.Errorf("unexpected exceptions %+v", bypass.Spec.Exceptions)
	}
}
