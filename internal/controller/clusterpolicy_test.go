package controller

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// The legacy bypass lists every ClusterPolicy that validates a chart-operator exception kind,
// including a rule that writes its kind as group/version/Kind, such as kyverno-app's
// restrict-polex-namespaces. It is rebuilt from the cluster on every reconcile.
func TestClusterPolicyBypass(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t, kyvernov1.Install, kyvernov2.Install)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		bypassClusterPolicy("restrict-polex-namespaces", "kyverno.io/v2/PolicyException"),
		bypassClusterPolicy("restrict-namespaces", "Namespace"),
		bypassClusterPolicy("restrict-pods", "Pod"),
		bypassClusterPolicy("restrict-namespace-labels", "Namespace"),
	).Build()
	// newReconciler starts without any state, like the operator after a restart.
	newReconciler := func() *ClusterPolicyReconciler {
		return &ClusterPolicyReconciler{
			Client:                      c,
			Scheme:                      s,
			Log:                         logr.Discard(),
			ChartOperatorExceptionKinds: []string{"Namespace", "PolicyException"},
			PolicyCache:                 map[string]kyvernov1.ClusterPolicy{},
			MaxJitterPercent:            10,
		}
	}
	reconcile := func(name string) {
		t.Helper()
		if _, err := newReconciler().Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	bypassKey := types.NamespacedName{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}
	bypassPolicies := func() []string {
		t.Helper()
		var bypass kyvernov2.PolicyException
		if err := c.Get(ctx, bypassKey, &bypass); err != nil {
			t.Fatalf("legacy chart-operator bypass not written: %v", err)
		}
		var names []string
		for _, exception := range bypass.Spec.Exceptions {
			names = append(names, exception.PolicyName)
		}
		return names
	}

	// Any reconcile, even of a ClusterPolicy that does not match, lists every matching one.
	reconcile("restrict-pods")
	if got, want := bypassPolicies(), []string{"restrict-namespace-labels", "restrict-namespaces", "restrict-polex-namespaces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bypass policies: got %v, want %v", got, want)
	}

	// A deleted ClusterPolicy is removed.
	if err := c.Delete(ctx, bypassClusterPolicy("restrict-namespaces", "Namespace")); err != nil {
		t.Fatal(err)
	}
	reconcile("restrict-namespaces")
	if got, want := bypassPolicies(), []string{"restrict-namespace-labels", "restrict-polex-namespaces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bypass policies after a delete: got %v, want %v", got, want)
	}

	// A ClusterPolicy edited so that it no longer matches is removed.
	edited := bypassClusterPolicy("restrict-namespace-labels", "Pod")
	var current kyvernov1.ClusterPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: edited.Name}, &current); err != nil {
		t.Fatal(err)
	}
	current.Spec = edited.Spec
	if err := c.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	reconcile("restrict-namespace-labels")
	if got, want := bypassPolicies(), []string{"restrict-polex-namespaces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bypass policies after an edit: got %v, want %v", got, want)
	}

	// Without matching ClusterPolicies the bypass is deleted.
	if err := c.Delete(ctx, bypassClusterPolicy("restrict-polex-namespaces", "kyverno.io/v2/PolicyException")); err != nil {
		t.Fatal(err)
	}
	reconcile("restrict-polex-namespaces")
	if err := c.Get(ctx, bypassKey, &kyvernov2.PolicyException{}); !apierrors.IsNotFound(err) {
		t.Errorf("legacy chart-operator bypass still exists: %v", err)
	}
}
