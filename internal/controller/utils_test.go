package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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

var managedKey = types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}

// managedCELException is a CEL PolicyException that KPO created.
func managedCELException() *policiesv1.PolicyException {
	return &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name: managedKey.Name, Namespace: managedKey.Namespace, Labels: map[string]string{ManagedBy: ComponentName},
	}}
}

// celExceptionRef names managedKey without any labels, as the reconcilers pass it to deleteManaged.
func celExceptionRef() *policiesv1.PolicyException {
	return &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: managedKey.Name, Namespace: managedKey.Namespace}}
}

func celOnlyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	return newScheme(t, policiesv1.Install)
}

// An object whose labels are removed between the check and the delete is checked again and kept,
// also with a client that decodes into the labels read by the first attempt.
func TestDeleteManagedKeepsAnObjectThatLostItsLabels(t *testing.T) {
	// arrange
	ctx := context.Background()
	deletes := 0
	funcs := relabelBeforeFirstDelete(nil, &deletes)
	funcs.Get = mergingGet().Get
	c := fake.NewClientBuilder().WithScheme(celOnlyScheme(t)).WithObjects(managedCELException()).WithInterceptorFuncs(funcs).Build()

	// act
	err := deleteManaged(ctx, c, celExceptionRef())

	// assert
	require.NoError(t, err)
	assert.Equal(t, 1, deletes)
	var kept policiesv1.PolicyException
	require.NoError(t, c.Get(ctx, managedKey, &kept), "the object was deleted")
	assert.Empty(t, kept.Labels)
}

// A delete that conflicts is retried, and the object is deleted once it is still managed.
func TestDeleteManagedRetriesAConflict(t *testing.T) {
	// arrange
	ctx := context.Background()
	deletes := 0
	c := fake.NewClientBuilder().WithScheme(celOnlyScheme(t)).WithObjects(managedCELException()).
		WithInterceptorFuncs(conflictingDeletes(1, &deletes)).Build()

	// act
	err := deleteManaged(ctx, c, celExceptionRef())

	// assert
	require.NoError(t, err)
	assert.Equal(t, 2, deletes)
	err = c.Get(ctx, managedKey, &policiesv1.PolicyException{})
	assert.True(t, apierrors.IsNotFound(err), "the object was not deleted: %v", err)
}

// An object that keeps changing is not deleted, and the conflict is returned.
func TestDeleteManagedGivesUpOnConstantConflicts(t *testing.T) {
	// arrange
	ctx := context.Background()
	deletes := 0
	c := fake.NewClientBuilder().WithScheme(celOnlyScheme(t)).WithObjects(managedCELException()).
		WithInterceptorFuncs(conflictingDeletes(1000, &deletes)).Build()

	// act
	err := deleteManaged(ctx, c, celExceptionRef())

	// assert
	assert.True(t, apierrors.IsConflict(err), "got %v, want a conflict", err)
	assert.Equal(t, retry.DefaultRetry.Steps, deletes)
	assert.NoError(t, c.Get(ctx, managedKey, &policiesv1.PolicyException{}), "the object was deleted")
}

func TestDeleteManagedGetFails(t *testing.T) {
	// arrange
	ctx := context.Background()
	errAPI := errors.New("API server unavailable")
	deletes := 0
	funcs := failGet[*policiesv1.PolicyException](errAPI)
	funcs.Delete = conflictingDeletes(0, &deletes).Delete
	c := fake.NewClientBuilder().WithScheme(celOnlyScheme(t)).WithObjects(managedCELException()).WithInterceptorFuncs(funcs).Build()

	// act
	err := deleteManaged(ctx, c, celExceptionRef())

	// assert
	require.ErrorIs(t, err, errAPI)
	assert.Zero(t, deletes)
}

// The legacy chart-operator bypass is deleted only when KPO manages it.
func TestDeleteLegacyChartOperatorBypass(t *testing.T) {
	key := types.NamespacedName{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}
	tests := []struct {
		name        string
		labels      map[string]string
		wantDeleted bool
	}{
		{"managed", map[string]string{ManagedBy: ComponentName}, true},
		{"not managed", map[string]string{ManagedBy: "someone-else"}, false},
		{"without labels", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// arrange
			ctx := context.Background()
			bypass := &kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: tt.labels}}
			c := fake.NewClientBuilder().WithScheme(newScheme(t, kyvernov2.Install)).WithObjects(bypass).Build()

			// act
			err := DeleteLegacyChartOperatorBypass(ctx, c)

			// assert
			require.NoError(t, err)
			err = c.Get(ctx, key, &kyvernov2.PolicyException{})
			assert.Equal(t, tt.wantDeleted, apierrors.IsNotFound(err), "get error: %v", err)
		})
	}
}
