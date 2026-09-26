package controller

import (
	"context"
	"errors"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDetectLegacyMode(t *testing.T) {
	both := []schema.GroupVersionKind{
		schema.GroupVersion(kyvernov1.GroupVersion).WithKind("ClusterPolicy"),
		schema.GroupVersion(kyvernov2.GroupVersion).WithKind("PolicyException"),
	}
	tests := []struct {
		name    string
		kinds   []schema.GroupVersionKind
		enabled bool
		want    LegacyMode
	}{
		{"legacy CRDs and switch on", both, true, LegacyWrite},
		{"legacy CRDs and switch off", both, false, LegacyCleanup},
		{"Kyverno 1.20 without legacy CRDs", nil, true, LegacyAbsent},
		{"only ClusterPolicy left", both[:1], true, LegacyAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper(nil)
			for _, gvk := range tt.kinds {
				mapper.Add(gvk, meta.RESTScopeNamespace)
			}
			got, err := DetectLegacyMode(mapper, tt.enabled)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// failingMapper fails every REST mapping with err, like a discovery call that times out.
type failingMapper struct {
	meta.RESTMapper
	err error
}

func (m failingMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, m.err
}

// A discovery error is returned instead of being taken for missing CRDs, which would switch legacy
// handling off.
func TestDetectLegacyModeDiscoveryFails(t *testing.T) {
	// arrange
	errDiscovery := errors.New("discovery timed out")

	// act
	_, err := DetectLegacyMode(failingMapper{err: errDiscovery}, true)

	// assert
	require.ErrorIs(t, err, errDiscovery)
}

func TestLegacyModeString(t *testing.T) {
	tests := []struct {
		mode LegacyMode
		want string
	}{
		{LegacyWrite, "write"},
		{LegacyCleanup, "cleanup"},
		{LegacyAbsent, "absent"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.mode.String())
		})
	}
}

// disallowPrivileged is the ClusterPolicy the test gspolex names.
func disallowPrivileged() *kyvernov1.ClusterPolicy {
	return &kyvernov1.ClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"},
		Spec:       kyvernov1.Spec{Rules: []kyvernov1.Rule{{Name: "privileged-containers"}}},
	}
}

// managedLegacyException is the legacy PolicyException KPO wrote for the test gspolex.
func managedLegacyException() *kyvernov2.PolicyException {
	return &kyvernov2.PolicyException{
		ObjectMeta: metav1.ObjectMeta{Name: managedKey.Name, Namespace: managedKey.Namespace, Labels: map[string]string{ManagedBy: ComponentName}},
		Spec: kyvernov2.PolicyExceptionSpec{
			Exceptions: []kyvernov2.Exception{{PolicyName: "disallow-privileged", RuleNames: []string{"privileged-containers"}}},
		},
	}
}

// migratedGSPolex is the test gspolex as exception-recommender writes it.
func migratedGSPolex() *policyAPI.PolicyException {
	g := testGSPolex()
	g.Labels = map[string]string{ManagedBy: SourceExceptionRecommender}
	g.Annotations = map[string]string{AnnotationMigratedFrom: "team-a/app"}
	return g
}

// When a lookup or a delete fails, reconcileLegacy returns the error, counts it, and leaves the
// existing legacy exception as it was: a missing entry would stop exempting that policy.
func TestReconcileLegacyErrors(t *testing.T) {
	errAPI := errors.New("API server unavailable")
	tests := []struct {
		name    string
		mode    LegacyMode
		gspolex func() *policyAPI.PolicyException
		funcs   interceptor.Funcs
		reason  string
	}{
		{"ClusterPolicy lookup fails", LegacyWrite, testGSPolex, failGet[*kyvernov1.ClusterPolicy](errAPI), "lookup_failed"},
		{"existing legacy exception lookup fails", LegacyWrite, testGSPolex, failGet[*kyvernov2.PolicyException](errAPI), "lookup_failed"},
		{"delete fails with legacy exceptions switched off", LegacyCleanup, testGSPolex, failDelete[*kyvernov2.PolicyException](errAPI), "delete_failed"},
		{"delete fails for a migrated gspolex", LegacyWrite, migratedGSPolex, failDelete[*kyvernov2.PolicyException](errAPI), "delete_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// arrange
			ctx := context.Background()
			s := newScheme(t, policyAPI.AddToScheme, kyvernov1.Install, kyvernov2.Install)
			gspolex := tt.gspolex()
			base := fake.NewClientBuilder().WithScheme(s).WithObjects(gspolex, disallowPrivileged(), managedLegacyException()).Build()
			var before kyvernov2.PolicyException
			require.NoError(t, base.Get(ctx, managedKey, &before))
			r := &PolicyExceptionReconciler{Client: interceptor.NewClient(base, tt.funcs), Scheme: s, Log: logr.Discard(), LegacyMode: tt.mode}
			errorsBefore := generationErrors(APILegacy, tt.reason)

			// act
			err := r.reconcileLegacy(ctx, gspolex, managedKey.Namespace)

			// assert
			require.ErrorIs(t, err, errAPI)
			assert.Equal(t, 1.0, generationErrors(APILegacy, tt.reason)-errorsBefore)
			var after kyvernov2.PolicyException
			require.NoError(t, base.Get(ctx, managedKey, &after), "the legacy exception was deleted")
			assert.Equal(t, before.ResourceVersion, after.ResourceVersion)
		})
	}
}
