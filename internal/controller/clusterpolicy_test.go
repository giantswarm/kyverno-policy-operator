package controller

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func bypassClusterPolicy(name, kind string) *kyvernov1.ClusterPolicy {
	return &kyvernov1.ClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kyvernov1.Spec{Rules: []kyvernov1.Rule{{
			Name: "restrict",
			MatchResources: kyvernov1.MatchResources{Any: kyvernov1.ResourceFilters{{
				ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{kind}},
			}}},
			Validation: &kyvernov1.Validation{Message: "restricted"},
		}}},
	}
}

// Every ClusterPolicy that validates a chart-operator exception kind keeps its entry in the legacy
// bypass, including a rule that writes its kind as group/version/Kind, such as kyverno-app's
// restrict-polex-namespaces. A deleted ClusterPolicy is left out of the next update.
func TestClusterPolicyBypass(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t, kyvernov1.Install, kyvernov2.Install)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		bypassClusterPolicy("restrict-polex-namespaces", "kyverno.io/v2/PolicyException"),
		bypassClusterPolicy("restrict-namespaces", "Namespace"),
		bypassClusterPolicy("restrict-pods", "Pod"),
	).Build()
	r := &ClusterPolicyReconciler{
		Client:                      c,
		Scheme:                      s,
		Log:                         logr.Discard(),
		ExceptionList:               map[string]kyvernov1.ClusterPolicy{},
		ChartOperatorExceptionKinds: []string{"Namespace", "PolicyException"},
		PolicyCache:                 map[string]kyvernov1.ClusterPolicy{},
		MaxJitterPercent:            10,
	}
	reconcile := func(name string) {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	bypassPolicies := func() []string {
		t.Helper()
		var bypass kyvernov2.PolicyException
		if err := c.Get(ctx, types.NamespacedName{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}, &bypass); err != nil {
			t.Fatalf("legacy chart-operator bypass not written: %v", err)
		}
		var names []string
		for _, exception := range bypass.Spec.Exceptions {
			names = append(names, exception.PolicyName)
		}
		return names
	}

	for _, name := range []string{"restrict-polex-namespaces", "restrict-namespaces", "restrict-pods"} {
		reconcile(name)
	}
	if got, want := bypassPolicies(), []string{"restrict-namespaces", "restrict-polex-namespaces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bypass policies: got %v, want %v", got, want)
	}

	if err := c.Delete(ctx, bypassClusterPolicy("restrict-namespaces", "Namespace")); err != nil {
		t.Fatal(err)
	}
	reconcile("restrict-namespaces")
	reconcile("restrict-polex-namespaces")
	if got, want := bypassPolicies(), []string{"restrict-polex-namespaces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bypass policies after a delete: got %v, want %v", got, want)
	}
}
