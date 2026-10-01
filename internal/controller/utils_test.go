package controller

import (
	"context"
	"reflect"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		{"reordered policies", []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a"}}, {PolicyName: "q"}}, []kyvernov2.Exception{{PolicyName: "q"}, {PolicyName: "p", RuleNames: []string{"a"}}}, true},
		{"duplicate policy", []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a"}}, {PolicyName: "p", RuleNames: []string{"a"}}}, []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a"}}, {PolicyName: "q", RuleNames: []string{"a"}}}, false},
		{"duplicate rule", []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a", "a"}}}, []kyvernov2.Exception{{PolicyName: "p", RuleNames: []string{"a", "b"}}}, false},
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

// An object that loses KPO's label between the check and the delete is checked again and kept.
func TestDeleteManagedRechecksAChangedObject(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	if err := policiesv1.Install(s); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}
	existing := &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{ManagedBy: ComponentName},
	}}
	deletes := 0
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			if deletes == 1 {
				// Someone else takes the object over after KPO checked it.
				var current policiesv1.PolicyException
				if err := c.Get(ctx, key, &current); err != nil {
					return err
				}
				current.Labels = map[string]string{ManagedBy: "someone-else"}
				if err := c.Update(ctx, &current); err != nil {
					return err
				}
			}
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()

	if err := deleteManaged(ctx, c, &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Errorf("got %d delete calls, want 1", deletes)
	}
	var kept policiesv1.PolicyException
	if err := c.Get(ctx, key, &kept); err != nil {
		t.Fatalf("the object was deleted: %v", err)
	}
	if kept.Labels[ManagedBy] != "someone-else" {
		t.Errorf("unexpected labels %v", kept.Labels)
	}
}

func TestDeleteManagedAlreadyDeleted(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	if err := policiesv1.Install(s); err != nil {
		t.Fatal(err)
	}
	existing := &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name: "app", Namespace: "policy-exceptions", Labels: map[string]string{ManagedBy: ComponentName},
	}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Group: "policies.kyverno.io", Resource: "policyexceptions"}, obj.GetName())
		},
	}).Build()

	if err := deleteManaged(ctx, c, &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "policy-exceptions"}}); err != nil {
		t.Errorf("got %v, want no error", err)
	}
}

func TestDropEmptyNames(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  []policyAPI.Target
	}{
		{"only an empty name leaves the target out", []string{""}, []policyAPI.Target{}},
		{"an empty name next to another is skipped", []string{"", "foo"}, []policyAPI.Target{{Kind: "Pod", Names: []string{"foo"}}}},
		{"names are kept", []string{"foo"}, []policyAPI.Target{{Kind: "Pod", Names: []string{"foo"}}}},
		{"no names are kept as they are", []string{}, []policyAPI.Target{{Kind: "Pod", Names: []string{}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dropEmptyNames(logr.Discard(), []policyAPI.Target{{Kind: "Pod", Names: tt.names}})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFormatNamesSkipsEmptyNames(t *testing.T) {
	if got, want := formatNames([]string{""}), []string{}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := formatNames([]string{"", "foo"}), []string{"foo*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
