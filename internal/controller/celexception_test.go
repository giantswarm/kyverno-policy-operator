package controller

import (
	"strings"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	kcompiler "github.com/kyverno/kyverno/pkg/cel/compiler"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/environment"
)

// eval evaluates a single match condition the way Kyverno does: object and request are bound,
// and either may be null. Optional types are enabled, as in Kyverno's and the API server's env.
func eval(t *testing.T, conds []admissionregistrationv1.MatchCondition, object, request any) bool {
	t.Helper()
	if len(conds) != 1 {
		t.Fatalf("want exactly one match condition, got %d", len(conds))
	}
	env, err := cel.NewEnv(cel.OptionalTypes(), cel.Variable("object", cel.DynType), cel.Variable("request", cel.DynType))
	if err != nil {
		t.Fatal(err)
	}
	ast, iss := env.Compile(conds[0].Expression)
	if iss.Err() != nil {
		t.Fatalf("compile %q: %v", conds[0].Expression, iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatal(err)
	}
	if object == nil {
		object = types.NullValue
	}
	if request == nil {
		request = types.NullValue
	}
	out, _, err := prg.Eval(map[string]any{"object": object, "request": request})
	if err != nil {
		t.Fatalf("eval %q: %v", conds[0].Expression, err)
	}
	return out.Value().(bool)
}

func obj(kind, namespace, name, generateName string) map[string]any {
	md := map[string]any{"namespace": namespace}
	if name != "" {
		md["name"] = name
	}
	if generateName != "" {
		md["generateName"] = generateName
	}
	return map[string]any{"kind": kind, "metadata": md}
}

func TestTargetsMatchConditions(t *testing.T) {
	long := strings.Repeat("a", 70)
	deployment := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"test-app-1"}}}
	twoNames := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"app-a", "app-b"}}}
	if expr := translateTargetsToMatchConditions(twoNames)[0].Expression; !strings.Contains(expr, `in ["app-a", "app-b"]`) {
		t.Errorf("two exact names should use an in list: %s", expr)
	}
	tests := []struct {
		name    string
		targets []policyAPI.Target
		object  any
		want    bool
	}{
		{"target deployment", deployment, obj("Deployment", "default", "test-app-1", ""), true},
		{"similar deployment name is not exempt", deployment, obj("Deployment", "default", "test-app-10", ""), false},
		{"replicaset of target", deployment, obj("ReplicaSet", "default", "test-app-1-5d4f8", ""), true},
		{"pod named by the API server", deployment, obj("Pod", "default", "", "test-app-1-5d4f8-"), true},
		{"pod of a similarly named deployment is not exempt", deployment, obj("Pod", "default", "", "test-app-10-5d4f8-"), false},
		{"pod in another namespace", deployment, obj("Pod", "other", "test-app-1-5d4f8-abcde", ""), false},
		{"other kind with the same name", deployment, obj("StatefulSet", "default", "test-app-1", ""), false},
		{"delete request has a null object", deployment, nil, false},
		{"user wildcard", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Deployment", "x", "app-foo", ""), true},
		{"user wildcard is anchored", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Deployment", "x", "myapp-foo", ""), false},
		{"pod of a user wildcard target", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Pod", "x", "", "app-foo-5d4f8-"), true},
		{"pod outside a user wildcard target", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Pod", "x", "other-x", ""), false},
		{"regex metacharacters are literal", []policyAPI.Target{{Kind: "Deployment", Names: []string{"a.b"}}}, obj("Deployment", "x", "axb", ""), false},
		{"long target name matches exactly", []policyAPI.Target{{Kind: "Deployment", Names: []string{long}}}, obj("Deployment", "x", long, ""), true},
		{"pods of long names use the truncated prefix", []policyAPI.Target{{Kind: "Deployment", Names: []string{long}}}, obj("Pod", "x", "", long[:58]), true},
		{"no names means any name", []policyAPI.Target{{Kind: "Pod", Namespaces: []string{"kube-system"}}}, obj("Pod", "kube-system", "anything", ""), true},
		{"namespace wildcard", []policyAPI.Target{{Kind: "Pod", Namespaces: []string{"team-*"}}}, obj("Pod", "team-a", "p", ""), true},
		{"no targets matches nothing", nil, obj("Pod", "default", "p", ""), false},
		{"second target", []policyAPI.Target{deployment[0], {Kind: "DaemonSet", Names: []string{"ds"}}}, obj("DaemonSet", "any", "ds", ""), true},
		{"first of two names", twoNames, obj("Deployment", "default", "app-a", ""), true},
		{"second of two names", twoNames, obj("Deployment", "default", "app-b", ""), true},
		{"pod of the second name", twoNames, obj("Pod", "default", "", "app-b-5d4f8-"), true},
		{"neither of two names", twoNames, obj("Deployment", "default", "app-c", ""), false},
		{"object without a namespace field", []policyAPI.Target{{Kind: "Namespace", Names: []string{"team-a"}}}, map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": "team-a"}}, true},
		{"namespace filter on an object without a namespace field", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"x"}}}, map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": "team-a"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eval(t, translateTargetsToMatchConditions(tt.targets), tt.object, nil); got != tt.want {
				t.Errorf("got %v, want %v; expression: %s", got, tt.want, translateTargetsToMatchConditions(tt.targets)[0].Expression)
			}
		})
	}
}

// TestTargetsMatchConditionsFormat pins the generated layout so that it stays readable in kubectl.
func TestTargetsMatchConditionsFormat(t *testing.T) {
	targets := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"test-app-1"}}}
	want := `object != null && (
  (object.metadata.?namespace.orValue("") == "default" && (
    (object.kind == "Deployment" && (object.metadata.?name.orValue("") != "" ? object.metadata.name : object.metadata.?generateName.orValue("")) == "test-app-1") ||
    (object.kind in ["ReplicaSet", "Pod"] && (object.metadata.?name.orValue("") != "" ? object.metadata.name : object.metadata.?generateName.orValue("")).startsWith("test-app-1-"))
  ))
)`
	got := translateTargetsToMatchConditions(targets)[0].Expression
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Errorf("trailing whitespace in %q", line)
		}
	}
}

func TestChartOperatorMatchConditions(t *testing.T) {
	conds := chartOperatorMatchConditions([]string{"Namespace", "PolicyException"})
	req := func(user, op string) map[string]any {
		return map[string]any{"userInfo": map[string]any{"username": user}, "operation": op}
	}
	ns := obj("Namespace", "", "team-a", "")
	tests := []struct {
		name    string
		object  any
		request any
		want    bool
	}{
		{"chart-operator creates a namespace", ns, req(ChartOperatorUsername, "CREATE"), true},
		{"chart-operator updates a namespace", ns, req(ChartOperatorUsername, "UPDATE"), true},
		{"chart-operator deletes", nil, req(ChartOperatorUsername, "DELETE"), false},
		{"another user", ns, req("system:serviceaccount:default:x", "CREATE"), false},
		{"another kind", obj("Pod", "giantswarm", "p", ""), req(ChartOperatorUsername, "CREATE"), false},
		{"background scan has no request", ns, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eval(t, conds, tt.object, tt.request); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// kyvernoExceptionEnv rebuilds the CEL environment Kyverno compiles ValidatingPolicy exception
// match conditions in: object and oldObject are dynamic, request is typed.
func kyvernoExceptionEnv(t *testing.T) *cel.Env {
	t.Helper()
	base, err := environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()).Env(environment.StoredExpressions)
	if err != nil {
		t.Fatal(err)
	}
	declOptions, err := apiservercel.NewDeclTypeProvider(kcompiler.RequestType).EnvOptions(base.CELTypeProvider())
	if err != nil {
		t.Fatal(err)
	}
	env, err := base.Extend(append(declOptions,
		cel.Variable(kcompiler.ObjectKey, cel.DynType),
		cel.Variable(kcompiler.OldObjectKey, cel.DynType),
		cel.Variable(kcompiler.RequestKey, kcompiler.RequestType.CelType()),
	)...)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// TestMatchConditionsCompileInKyvernoEnv compiles the generated conditions in the environment
// Kyverno builds for ValidatingPolicy exception match conditions, whose "request" is typed,
// unlike cel.DynType above.
func TestMatchConditionsCompileInKyvernoEnv(t *testing.T) {
	env := kyvernoExceptionEnv(t)
	for name, conds := range map[string][]admissionregistrationv1.MatchCondition{
		"targets":        translateTargetsToMatchConditions([]policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default", "team-*"}, Names: []string{"a", "b-*", "c?d"}}}),
		"no targets":     translateTargetsToMatchConditions(nil),
		"chart-operator": chartOperatorMatchConditions([]string{"Namespace"}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, errs := kcompiler.CompileMatchConditions(field.NewPath("spec", "matchConditions"), env, conds...); len(errs) > 0 {
				t.Fatalf("kyverno rejected %q: %v", conds[0].Expression, errs.ToAggregate())
			}
		})
	}
}
