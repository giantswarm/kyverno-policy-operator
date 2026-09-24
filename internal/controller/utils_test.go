package controller

import (
	"context"
	"reflect"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestIsMigrated(t *testing.T) {
	annotation := map[string]string{AnnotationMigratedFrom: "team-a/foo"}
	label := map[string]string{ManagedBy: "exception-recommender"}
	tests := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		want        bool
	}{
		{"annotation and label", annotation, label, true},
		{"annotation only", annotation, nil, false},
		{"label only", nil, label, false},
		{"annotation and another managed-by", annotation, map[string]string{ManagedBy: "flux"}, false},
		{"neither", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations, Labels: tt.labels}}
			if got := isMigrated(p); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnorderedEqual(t *testing.T) {
	one := []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a"}}}
	tests := []struct {
		name      string
		got, want []kyvernov2.Exception
		equal     bool
	}{
		{"same", one, one, true},
		{"reordered rules", []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a", "b"}}}, []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"b", "a"}}}, true},
		{"rule added, for example by autogen", one, []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a", "autogen-a"}}}, false},
		{"rule removed", []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a", "b"}}}, one, false},
		{"other policy", one, []kyvernov2.Exception{{PolicyName: "q", RuleNames: []string{"a"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unorderedEqual(tt.got, tt.want); got != tt.equal {
				t.Errorf("got %v, want %v", got, tt.equal)
			}
		})
	}
}

// Every ClusterPolicy event, including a status.autogen update, enqueues the gspolexes that name it.
func TestGspolexesForClusterPolicy(t *testing.T) {
	gspolex := func(name string, policies ...string) *policyAPI.PolicyException {
		return &policyAPI.PolicyException{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "policy-exceptions"},
			Spec:       policyAPI.PolicyExceptionSpec{Policies: policies},
		}
	}
	s := runtime.NewScheme()
	if err := policyAPI.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		gspolex("a", "disallow-privileged"),
		gspolex("b", "require-labels"),
		gspolex("c", "require-labels", "disallow-privileged"),
	).Build()
	r := &PolicyExceptionReconciler{Client: c}

	clusterPolicy := &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"}}
	clusterPolicy.Status.Autogen.Rules = []kyvernov1.Rule{{Name: "autogen-restrict"}}
	got := r.gspolexesForClusterPolicy(context.Background(), clusterPolicy)
	want := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: "policy-exceptions", Name: "a"}},
		{NamespacedName: types.NamespacedName{Namespace: "policy-exceptions", Name: "c"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := r.gspolexesForClusterPolicy(context.Background(), &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "unused"}}); len(got) != 0 {
		t.Errorf("got %v for a ClusterPolicy no gspolex names", got)
	}
}
