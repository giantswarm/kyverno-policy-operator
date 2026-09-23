package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func managedMeta(name, source string, annotations map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: "policy-exceptions", Annotations: annotations,
		Labels: map[string]string{ManagedBy: ComponentName, LabelSource: source}}
}

func TestExceptionCollector(t *testing.T) {
	s := runtime.NewScheme()
	if err := policiesv1.Install(s); err != nil {
		t.Fatal(err)
	}
	if err := kyvernov2.Install(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		&policiesv1.PolicyException{ObjectMeta: managedMeta("a", SourceGSPolex, nil)},
		&policiesv1.PolicyException{ObjectMeta: managedMeta("b", SourceGSPolex, map[string]string{AnnotationUnresolvedPolicies: "p1,p2"})},
		&policiesv1.PolicyException{ObjectMeta: managedMeta("c-migrated", SourceExceptionRecommender, map[string]string{AnnotationMigratedFrom: "x/c"})},
		&kyvernov2.PolicyException{ObjectMeta: managedMeta("a", SourceGSPolex, nil)},
		&kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: "hand-written", Namespace: "x"}},
	).Build()

	collector := &ExceptionCollector{Reader: c, LegacyMode: LegacyWrite, Log: logr.Discard()}
	expected := `
# HELP kyverno_policy_operator_dual_policyexceptions Generated exceptions that exist both as kyverno.io/v2 and policies.kyverno.io PolicyExceptions.
# TYPE kyverno_policy_operator_dual_policyexceptions gauge
kyverno_policy_operator_dual_policyexceptions{source="gspolex"} 1
# HELP kyverno_policy_operator_legacy_exceptions_enabled Whether kyverno.io/v2 PolicyExceptions are being written (1) or not (0).
# TYPE kyverno_policy_operator_legacy_exceptions_enabled gauge
kyverno_policy_operator_legacy_exceptions_enabled 1
# HELP kyverno_policy_operator_policyexceptions Generated Kyverno PolicyExceptions by API and source.
# TYPE kyverno_policy_operator_policyexceptions gauge
kyverno_policy_operator_policyexceptions{api="cel",source="exception-recommender"} 1
kyverno_policy_operator_policyexceptions{api="cel",source="gspolex"} 2
kyverno_policy_operator_policyexceptions{api="legacy",source="gspolex"} 1
# HELP kyverno_policy_operator_unresolved_policy_refs Generated CEL exceptions referencing a policy name that matches no CEL policy.
# TYPE kyverno_policy_operator_unresolved_policy_refs gauge
kyverno_policy_operator_unresolved_policy_refs{policy="p1"} 1
kyverno_policy_operator_unresolved_policy_refs{policy="p2"} 1
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestExceptionCollectorLegacyListFails(t *testing.T) {
	s := runtime.NewScheme()
	if err := policiesv1.Install(s); err != nil {
		t.Fatal(err)
	}
	if err := kyvernov2.Install(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		&policiesv1.PolicyException{ObjectMeta: managedMeta("a", SourceGSPolex, nil)},
		&kyvernov2.PolicyException{ObjectMeta: managedMeta("a", SourceGSPolex, nil)},
	).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*kyvernov2.PolicyExceptionList); ok {
				return errors.New("legacy list failed")
			}
			return c.List(ctx, list, opts...)
		},
	}).Build()

	collector := &ExceptionCollector{Reader: c, LegacyMode: LegacyWrite, Log: logr.Discard()}
	expected := `
# HELP kyverno_policy_operator_legacy_exceptions_enabled Whether kyverno.io/v2 PolicyExceptions are being written (1) or not (0).
# TYPE kyverno_policy_operator_legacy_exceptions_enabled gauge
kyverno_policy_operator_legacy_exceptions_enabled 1
# HELP kyverno_policy_operator_policyexceptions Generated Kyverno PolicyExceptions by API and source.
# TYPE kyverno_policy_operator_policyexceptions gauge
kyverno_policy_operator_policyexceptions{api="cel",source="gspolex"} 1
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}
