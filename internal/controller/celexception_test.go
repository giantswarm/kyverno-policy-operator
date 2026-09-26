package controller

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	kcompiler "github.com/kyverno/kyverno/pkg/cel/compiler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// withAPIVersion sets the apiVersion of an object built by obj.
func withAPIVersion(o map[string]any, apiVersion string) map[string]any {
	o["apiVersion"] = apiVersion
	return o
}

func TestTargetsMatchConditions(t *testing.T) {
	long := strings.Repeat("a", 70)
	deployment := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"test-app-1"}}}
	twoNames := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"app-a", "app-b"}}}
	middleGlob := []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*-web"}}}
	singleChar := []policyAPI.Target{{Kind: "Deployment", Names: []string{"a?c"}}}
	mixed := []policyAPI.Target{{Kind: "Deployment", Names: []string{"api", "worker", "web-*", "db-?-x"}}}
	gvk := []policyAPI.Target{{Kind: "apps/v1/Deployment", Names: []string{"app"}}}
	versionKind := []policyAPI.Target{{Kind: "v1/Pod", Names: []string{"p"}}}
	anyKind := []policyAPI.Target{{Kind: "*", Names: []string{"app"}}}
	cronJob := []policyAPI.Target{{Kind: "CronJob", Namespaces: []string{"x"}, Names: []string{"backup"}}}
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
		// Lossy on purpose: the legacy exception also exempts DELETE, but a DELETE has no object.
		{"delete request has a null object", deployment, nil, false},
		{"user wildcard", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Deployment", "x", "app-foo", ""), true},
		{"user wildcard is anchored", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Deployment", "x", "myapp-foo", ""), false},
		{"pod of a user wildcard target", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Pod", "x", "", "app-foo-5d4f8-"), true},
		{"pod outside a user wildcard target", []policyAPI.Target{{Kind: "Deployment", Names: []string{"app-*"}}}, obj("Pod", "x", "other-x", ""), false},
		{"quote and backslash in a name are literal", []policyAPI.Target{{Kind: "Deployment", Names: []string{`a"b\c`}}}, obj("Deployment", "x", `a"b\c`, ""), true},
		{"quote and backslash in a name match nothing else", []policyAPI.Target{{Kind: "Deployment", Names: []string{`a"b\c`}}}, obj("Deployment", "x", `a"bc`, ""), false},
		{"quote and backslash in a wildcard name are literal", []policyAPI.Target{{Kind: "Deployment", Names: []string{`a"b\?`}}}, obj("Deployment", "x", `a"b\c`, ""), true},
		{"quote and backslash in a prefix name are literal", []policyAPI.Target{{Kind: "Pod", Names: []string{`a"b\`}}}, obj("Pod", "x", `a"b\c`, ""), true},
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
		{"middle wildcard", middleGlob, obj("Deployment", "x", "app-x-web", ""), true},
		{"middle wildcard is anchored at the start", middleGlob, obj("Deployment", "x", "myapp-x-web", ""), false},
		{"middle wildcard is anchored at the end", middleGlob, obj("Deployment", "x", "app-x-web2", ""), false},
		{"? matches one character", singleChar, obj("Deployment", "x", "abc", ""), true},
		{"? does not match zero characters", singleChar, obj("Deployment", "x", "ac", ""), false},
		{"? does not match two characters", singleChar, obj("Deployment", "x", "abbc", ""), false},
		{"mixed names: exact", mixed, obj("Deployment", "x", "api", ""), true},
		{"mixed names: second exact", mixed, obj("Deployment", "x", "worker", ""), true},
		{"mixed names: trailing wildcard", mixed, obj("Deployment", "x", "web-1", ""), true},
		{"mixed names: middle glob", mixed, obj("Deployment", "x", "db-a-x", ""), true},
		{"mixed names: none", mixed, obj("Deployment", "x", "db-ab-x", ""), false},
		{"group/version/kind", gvk, withAPIVersion(obj("Deployment", "x", "app", ""), "apps/v1"), true},
		{"group/version/kind in another group", gvk, withAPIVersion(obj("Deployment", "x", "app", ""), "extensions/v1beta1"), false},
		{"replicaset of a group/version/kind target", gvk, withAPIVersion(obj("ReplicaSet", "x", "app-5d4f8", ""), "apps/v1"), true},
		{"pod of a group/version/kind target", gvk, withAPIVersion(obj("Pod", "x", "", "app-5d4f8-"), "v1"), true},
		{"version/kind in the core group", versionKind, withAPIVersion(obj("Pod", "x", "p", ""), "v1"), true},
		{"version/kind in any group", versionKind, withAPIVersion(obj("Pod", "x", "p", ""), "example.com/v1"), true},
		{"version/kind with another version", versionKind, withAPIVersion(obj("Pod", "x", "p", ""), "v2"), false},
		{"wildcard version", []policyAPI.Target{{Kind: "apps/*/Deployment", Names: []string{"app"}}}, withAPIVersion(obj("Deployment", "x", "app", ""), "apps/v1"), true},
		{"wildcard version in another group", []policyAPI.Target{{Kind: "apps/*/Deployment", Names: []string{"app"}}}, withAPIVersion(obj("Deployment", "x", "app", ""), "example.com/v1"), false},
		{"core group, wildcard version", []policyAPI.Target{{Kind: "/*/Pod"}}, withAPIVersion(obj("Pod", "x", "p", ""), "v1"), true},
		{"core group, wildcard version, in another group", []policyAPI.Target{{Kind: "/*/Pod"}}, withAPIVersion(obj("Pod", "x", "p", ""), "example.com/v1"), false},
		{"core group, version wildcard", []policyAPI.Target{{Kind: "/v*/Pod"}}, withAPIVersion(obj("Pod", "x", "p", ""), "v1"), true},
		{"core group, version wildcard, in another group", []policyAPI.Target{{Kind: "/v*/Pod"}}, withAPIVersion(obj("Pod", "x", "p", ""), "velero.io/v1"), false},
		{"core group, exact version", []policyAPI.Target{{Kind: "/v1/Pod"}}, withAPIVersion(obj("Pod", "x", "p", ""), "v1"), true},
		{"any kind", anyKind, withAPIVersion(obj("ConfigMap", "x", "app", ""), "v1"), true},
		{"any kind, other name", anyKind, withAPIVersion(obj("ConfigMap", "x", "other", ""), "v1"), false},
		{"pods of an any-kind target", anyKind, obj("Pod", "x", "", "app-5d4f8-"), true},
		{"any kind without names or namespaces", []policyAPI.Target{{Kind: "*"}}, obj("Secret", "x", "s", ""), true},
		{"kind wildcard", []policyAPI.Target{{Kind: "Deploy*", Names: []string{"app"}}}, obj("Deployment", "x", "app", ""), true},
		{"pod target matches its generated names", []policyAPI.Target{{Kind: "Pod", Names: []string{"my-agent"}}}, obj("Pod", "x", "my-agent-7f9c4-abcde", ""), true},
		{"pod target matches a pod named by the API server", []policyAPI.Target{{Kind: "Pod", Names: []string{"my-agent"}}}, obj("Pod", "x", "", "my-agent-7f9c4-"), true},
		{"pod target does not match another prefix", []policyAPI.Target{{Kind: "Pod", Names: []string{"my-agent"}}}, obj("Pod", "x", "other-my-agent", ""), false},
		{"version/kind pod target matches by prefix", []policyAPI.Target{{Kind: "v1/Pod", Names: []string{"my-agent"}}}, withAPIVersion(obj("Pod", "x", "my-agent-7f9c4-abcde", ""), "v1"), true},
		{"replicaset target matches by prefix", []policyAPI.Target{{Kind: "ReplicaSet", Names: []string{"web"}}}, obj("ReplicaSet", "x", "web-5d4f8", ""), true},
		{"job target matches by prefix", []policyAPI.Target{{Kind: "Job", Names: []string{"backup"}}}, obj("Job", "x", "backup-28391820", ""), true},
		{"long pod target names use the truncated prefix", []policyAPI.Target{{Kind: "Pod", Names: []string{long}}}, obj("Pod", "x", "", long[:58]), true},
		{"pod target with a wildcard matches like legacy", []policyAPI.Target{{Kind: "Pod", Names: []string{"a?c"}}}, obj("Pod", "x", "abcd", ""), true},
		{"pod target with a wildcard is anchored at the start", []policyAPI.Target{{Kind: "Pod", Names: []string{"a?c"}}}, obj("Pod", "x", "xabc", ""), false},
		{"deployment target does not match by prefix", []policyAPI.Target{{Kind: "Deployment", Names: []string{"web"}}}, obj("Deployment", "x", "web2", ""), false},
		{"kind wildcard does not match another kind", []policyAPI.Target{{Kind: "Deploy*", Names: []string{"app"}}}, obj("DaemonSet", "x", "app", ""), false},
		{"*/v1/Deployment is a subresource and left out", []policyAPI.Target{{Kind: "*/v1/Deployment", Names: []string{"app"}}}, withAPIVersion(obj("Deployment", "x", "app", ""), "apps/v1"), false},
		{"apps/Deployment is a subresource and left out", []policyAPI.Target{{Kind: "apps/Deployment", Names: []string{"app"}}}, withAPIVersion(obj("Deployment", "x", "app", ""), "apps/v1"), false},
		{"subresource target is left out", []policyAPI.Target{{Kind: "Pod/exec", Names: []string{"p"}}}, obj("Pod", "x", "p", ""), false},
		{"other targets still match next to a subresource target", []policyAPI.Target{{Kind: "Pod/exec"}, {Kind: "Pod", Names: []string{"p"}}}, obj("Pod", "x", "p", ""), true},
		{"namespace target matches the namespace by name", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"team-a"}}}, obj("Namespace", "", "team-a", ""), true},
		{"namespace target does not match another namespace", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"team-a"}}}, obj("Namespace", "", "team-b", ""), false},
		{"namespace target with a namespace wildcard", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"team-*"}}}, obj("Namespace", "", "team-a", ""), true},
		{"namespace wildcard on a namespace target is anchored", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"team-*"}}}, obj("Namespace", "", "my-team-a", ""), false},
		{"namespaced kinds still use their namespace", []policyAPI.Target{{Kind: "Pod", Namespaces: []string{"team-a"}}}, obj("Pod", "team-b", "team-a", ""), false},
		{"object without a namespace field", []policyAPI.Target{{Kind: "Namespace", Names: []string{"team-a"}}}, map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": "team-a"}}, true},
		{"cronjob target", cronJob, obj("CronJob", "x", "backup", ""), true},
		{"job of a cronjob target", cronJob, obj("Job", "x", "backup-28391820", ""), true},
		{"pod of a cronjob target", cronJob, obj("Pod", "x", "", "backup-28391820-"), true},
		{"job of a similarly named cronjob is not exempt", cronJob, obj("Job", "x", "backup2-28391820", ""), false},
		// Known: "<name>-" also covers the pods of another Deployment whose name starts with "<name>-".
		{"pod of a deployment named with the target name as prefix", deployment, obj("Pod", "default", "", "test-app-1-api-5d4f8-"), true},
		{"quote and backslash in a namespace are literal", []policyAPI.Target{{Kind: "Pod", Namespaces: []string{`n"s\`}}}, obj("Pod", `n"s\`, "p", ""), true},
		{"quote and backslash in a namespace match nothing else", []policyAPI.Target{{Kind: "Pod", Namespaces: []string{`n"s\`}}}, obj("Pod", `n"s`, "p", ""), false},
		{"quote in a kind is literal", []policyAPI.Target{{Kind: `Po"d`}}, obj(`Po"d`, "x", "p", ""), true},
		{"quote in a kind matches nothing else", []policyAPI.Target{{Kind: `Po"d`}}, obj("Po", "x", "p", ""), false},
		{"a name cannot inject CEL", []policyAPI.Target{{Kind: "ConfigMap", Names: []string{`a" || true || "`}}}, obj("ConfigMap", "x", "other", ""), false},
		{"a namespace cannot inject CEL", []policyAPI.Target{{Kind: "ConfigMap", Namespaces: []string{`a" || true || "`}}}, obj("ConfigMap", "x", "c", ""), false},
		{"a wildcard name cannot inject CEL", []policyAPI.Target{{Kind: "ConfigMap", Names: []string{`a" || true || "?`}}}, obj("ConfigMap", "x", "other", ""), false},
		{"name * matches any name", []policyAPI.Target{{Kind: "ConfigMap", Names: []string{"*"}}}, obj("ConfigMap", "x", "anything", ""), true},
		{"namespace * matches any namespace", []policyAPI.Target{{Kind: "ConfigMap", Namespaces: []string{"*"}}}, obj("ConfigMap", "anywhere", "c", ""), true},
		{"namespace * also matches a cluster-scoped object", []policyAPI.Target{{Kind: "*", Namespaces: []string{"*"}}}, obj("ClusterRole", "", "c", ""), true},
		{"namespace filter on an object without a namespace field", []policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"x"}}}, map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": "team-a"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eval(t, translateTargetsToMatchConditions(tt.targets, false), tt.object, nil); got != tt.want {
				t.Errorf("got %v, want %v; expression: %s", got, tt.want, translateTargetsToMatchConditions(tt.targets, false)[0].Expression)
			}
		})
	}
}

// TestBridgeMatchConditions covers gspolexes migrated by exception-recommender, whose targets
// already list every kind the legacy exception covered, so no kinds are derived.
func TestBridgeMatchConditions(t *testing.T) {
	deployment := []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"web"}}}
	debugPod := []policyAPI.Target{{Kind: "Pod", Names: []string{"debug"}}}
	debugPods := []policyAPI.Target{{Kind: "Pod", Names: []string{"debug-*"}}}
	tests := []struct {
		name    string
		targets []policyAPI.Target
		bridge  bool
		object  any
		want    bool
	}{
		{"bridge matches the deployment", deployment, true, obj("Deployment", "default", "web", ""), true},
		{"bridge does not match the deployment's replicasets", deployment, true, obj("ReplicaSet", "default", "web-5d4f8", ""), false},
		{"bridge does not match the deployment's pods", deployment, true, obj("Pod", "default", "", "web-5d4f8-"), false},
		{"non-bridge matches the deployment's replicasets", deployment, false, obj("ReplicaSet", "default", "web-5d4f8", ""), true},
		{"non-bridge matches the deployment's pods", deployment, false, obj("Pod", "default", "", "web-5d4f8-"), true},
		{"bridge pod name is exact", debugPod, true, obj("Pod", "x", "debug", ""), true},
		{"bridge pod name is not a prefix", debugPod, true, obj("Pod", "x", "debugger", ""), false},
		{"bridge pod name does not match its generated names", debugPod, true, obj("Pod", "x", "debug-x", ""), false},
		{"non-bridge pod name is a prefix", debugPod, false, obj("Pod", "x", "debugger", ""), true},
		{"bridge pod glob is kept", debugPods, true, obj("Pod", "x", "debug-x", ""), true},
		{"bridge pod glob is anchored", debugPods, true, obj("Pod", "x", "debug", ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conds := translateTargetsToMatchConditions(tt.targets, tt.bridge)
			if got := eval(t, conds, tt.object, nil); got != tt.want {
				t.Errorf("got %v, want %v; expression: %s", got, tt.want, conds[0].Expression)
			}
		})
	}
}

// TestBridgeTargetNames covers the names of Pod, ReplicaSet and Job targets in a bridge. The legacy
// exception it was migrated from matched them as written, so the bridge adds no trailing "*" and
// does not cut them to 58 characters.
func TestBridgeTargetNames(t *testing.T) {
	long := strings.Repeat("a", 70)
	podWeb := policyAPI.Target{Kind: "Pod", Names: []string{"web"}}
	podLong := policyAPI.Target{Kind: "Pod", Names: []string{long}}
	podWildcard := policyAPI.Target{Kind: "Pod", Names: []string{"web-*"}}
	tests := []struct {
		name   string
		target policyAPI.Target
		object map[string]any
		want   bool
	}{
		{"pod with the exact name", podWeb, obj("Pod", "x", "web", ""), true},
		{"pod with a longer name", podWeb, obj("Pod", "x", "webhook", ""), false},
		{"pod with a generated suffix", podWeb, obj("Pod", "x", "web-7f9c4-abcde", ""), false},
		{"pod named by the API server", podWeb, obj("Pod", "x", "", "web-"), false},
		{"replicaset with a longer name", policyAPI.Target{Kind: "ReplicaSet", Names: []string{"web"}}, obj("ReplicaSet", "x", "web-evil", ""), false},
		{"group/version/kind job with a longer name", policyAPI.Target{Kind: "batch/v1/Job", Names: []string{"web"}}, withAPIVersion(obj("Job", "x", "web2", ""), "batch/v1"), false},
		{"version/kind pod with a longer name", policyAPI.Target{Kind: "v1/Pod", Names: []string{"web"}}, withAPIVersion(obj("Pod", "x", "webhook", ""), "v1"), false},
		{"long name matches itself", podLong, obj("Pod", "x", long, ""), true},
		{"long name is not cut to 58 characters", podLong, obj("Pod", "x", long[:58]+"zz", ""), false},
		{"user wildcard", podWildcard, obj("Pod", "x", "web-1", ""), true},
		{"user wildcard is anchored", podWildcard, obj("Pod", "x", "webhook", ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// arrange
			conds := translateTargetsToMatchConditions([]policyAPI.Target{tt.target}, true)

			// act
			got := eval(t, conds, tt.object, nil)

			// assert
			assert.Equal(t, tt.want, got, "expression: %s", conds[0].Expression)
		})
	}
}

// TestLossyTranslations lists the objects the legacy exception of the same gspolex exempts
// through its "name*" patterns, but the CEL exception does not. The CEL matching is stricter on
// purpose, except for subresource targets, which it cannot express.
func TestLossyTranslations(t *testing.T) {
	long := strings.Repeat("a", 70)
	tests := []struct {
		name        string
		target      policyAPI.Target
		object      map[string]any
		legacyNames []string
	}{
		{"deployment with a longer name", policyAPI.Target{Kind: "Deployment", Names: []string{"test-app-1"}}, obj("Deployment", "x", "test-app-10", ""), []string{"test-app-1*"}},
		{"pod of a deployment with a longer name", policyAPI.Target{Kind: "Deployment", Names: []string{"test-app-1"}}, obj("Pod", "x", "", "test-app-10-5d4f8-"), []string{"test-app-1*"}},
		{"long name that differs after character 58", policyAPI.Target{Kind: "Deployment", Names: []string{long}}, obj("Deployment", "x", long[:58]+"zz", ""), []string{long[:58] + "*"}},
		// The legacy exception exempts Pods "p*" of a Pod/exec target, the CEL exception drops the target.
		{"pod of a subresource target", policyAPI.Target{Kind: "Pod/exec", Names: []string{"p"}}, obj("Pod", "x", "p-1", ""), []string{"p*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := []policyAPI.Target{tt.target}
			if got := eval(t, translateTargetsToMatchConditions(targets, false), tt.object, nil); got {
				t.Errorf("the CEL exception exempts %v", tt.object)
			}
			legacy := translateTargetsToResourceFilters(targets)[0]
			if !reflect.DeepEqual(legacy.Names, tt.legacyNames) {
				t.Errorf("legacy names: got %v, want %v", legacy.Names, tt.legacyNames)
			}
			if kind := tt.object["kind"].(string); !slices.Contains(legacy.Kinds, kind) {
				t.Errorf("legacy kinds %v do not cover %v", legacy.Kinds, kind)
			}
		})
	}
}

// A group/version/kind Deployment target derives ReplicaSets in the CEL exception, but not in the
// legacy one, whose derived kinds are looked up by the whole "apps/v1/Deployment" string.
func TestGroupVersionKindTargetDerivesReplicaSetsOnlyInCEL(t *testing.T) {
	// arrange
	targets := []policyAPI.Target{{Kind: "apps/v1/Deployment", Names: []string{"web"}}}
	replicaSet := withAPIVersion(obj("ReplicaSet", "x", "web-5d4f8", ""), "apps/v1")

	// act
	cel := eval(t, translateTargetsToMatchConditions(targets, false), replicaSet, nil)
	legacy := translateTargetsToResourceFilters(targets)[0]

	// assert
	assert.True(t, cel, "the CEL exception does not cover the Deployment's ReplicaSets")
	assert.Equal(t, []string{"apps/v1/Deployment", "Pod"}, legacy.Kinds)
}

func TestUnsupportedTargetKinds(t *testing.T) {
	targets := []policyAPI.Target{{Kind: "Pod"}, {Kind: "Pod/exec"}, {Kind: "v1/Pod/log"}, {Kind: "*/*"}, {Kind: "apps/v1/Deployment"}, {Kind: "*/v1/Deployment"}, {Kind: "apps/Deployment"}, {Kind: ""}}
	want := []string{"Pod/exec", "v1/Pod/log", "*/*", "*/v1/Deployment", "apps/Deployment", ""}
	if got := unsupportedTargetKinds(targets); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTargetsMatchConditionsFormat pins the generated layout so that it stays readable in kubectl.
func TestTargetsMatchConditionsFormat(t *testing.T) {
	const name = `(object.metadata.?name.orValue("") != "" ? object.metadata.name : object.metadata.?generateName.orValue(""))`
	const namespace = `(object.kind == "Namespace" ? object.metadata.?name.orValue("") : object.metadata.?namespace.orValue(""))`
	tests := []struct {
		name    string
		targets []policyAPI.Target
		bridge  bool
		want    string
	}{
		{
			name:    "one namespaced target",
			targets: []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default"}, Names: []string{"test-app-1"}}},
			want: `object != null && (
  (` + namespace + ` == "default" && (
    (object.kind == "Deployment" && ` + name + ` == "test-app-1") ||
    (object.kind in ["ReplicaSet", "Pod"] && ` + name + `.startsWith("test-app-1-"))
  ))
)`,
		},
		{
			name: "two targets without namespaces",
			targets: []policyAPI.Target{
				{Kind: "Deployment", Names: []string{"web"}},
				{Kind: "Pod", Names: []string{"debug"}},
			},
			want: `object != null && (
  (
    (object.kind == "Deployment" && ` + name + ` == "web") ||
    (object.kind in ["ReplicaSet", "Pod"] && ` + name + `.startsWith("web-"))
  ) ||
  (object.kind == "Pod" && ` + name + `.startsWith("debug"))
)`,
		},
		{
			name:    "group/version/kind",
			targets: []policyAPI.Target{{Kind: "apps/v1/Deployment", Names: []string{"web"}}},
			want: `object != null && (
  (
    (object.apiVersion == "apps/v1" && object.kind == "Deployment" && ` + name + ` == "web") ||
    (object.kind in ["ReplicaSet", "Pod"] && ` + name + `.startsWith("web-"))
  )
)`,
		},
		{
			name:    "version/kind",
			targets: []policyAPI.Target{{Kind: "v1/Pod", Namespaces: []string{"default"}}},
			want: `object != null && (
  (` + namespace + ` == "default" && (object.apiVersion == "v1" || object.apiVersion.matches("^.*/v1$")) && object.kind == "Pod")
)`,
		},
		{
			name:    "any kind",
			targets: []policyAPI.Target{{Kind: "*", Names: []string{"web"}}},
			want: `object != null && (
  (
    (` + name + ` == "web") ||
    (object.kind == "Pod" && ` + name + `.startsWith("web-"))
  )
)`,
		},
		{
			name:    "bridge pod",
			targets: []policyAPI.Target{{Kind: "Pod", Names: []string{"debug", "debug-*"}}},
			bridge:  true,
			want: `object != null && (
  (object.kind == "Pod" && (` + name + ` == "debug" || ` + name + `.startsWith("debug-")))
)`,
		},
		{
			name:    "bridge",
			targets: []policyAPI.Target{{Kind: "Deployment", Names: []string{"web"}}},
			bridge:  true,
			want: `object != null && (
  (object.kind == "Deployment" && ` + name + ` == "web")
)`,
		},
		{
			name:    "bridge pod",
			targets: []policyAPI.Target{{Kind: "Pod", Names: []string{"web"}}},
			bridge:  true,
			want: `object != null && (
  (object.kind == "Pod" && ` + name + ` == "web")
)`,
		},
		{
			name:    "a subresource target is left out",
			targets: []policyAPI.Target{{Kind: "Pod/exec", Names: []string{"web"}}},
			want:    `false`,
		},
		{
			name:    "two pod names",
			targets: []policyAPI.Target{{Kind: "Pod", Namespaces: []string{"default"}, Names: []string{"app-a", "app-b"}}},
			want: `object != null && (
  (` + namespace + ` == "default" && object.kind == "Pod" && (` + name + `.startsWith("app-a") || ` + name + `.startsWith("app-b")))
)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateTargetsToMatchConditions(tt.targets, tt.bridge)[0].Expression
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			for _, line := range strings.Split(got, "\n") {
				if strings.TrimRight(line, " \t") != line {
					t.Errorf("trailing whitespace in %q", line)
				}
			}
		})
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
		{"chart-operator deletes with an object", ns, req(ChartOperatorUsername, "DELETE"), false},
		{"chart-operator connects", ns, req(ChartOperatorUsername, "CONNECT"), false},
		{"request without a user", ns, map[string]any{"operation": "CREATE"}, false},
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

// A background scan evaluates the bypass with a null request. In Kyverno's typed environment, reading
// request.userInfo from a null request is an error, so the condition must check request first.
func TestChartOperatorMatchConditionsWithoutRequestInKyvernoEnv(t *testing.T) {
	// arrange
	env := kyvernoExceptionEnv(t)
	ast, iss := env.Compile(chartOperatorMatchConditions([]string{"Namespace"})[0].Expression)
	require.NoError(t, iss.Err())
	prg, err := env.Program(ast)
	require.NoError(t, err)

	// act
	out, _, err := prg.Eval(map[string]any{"object": obj("Namespace", "", "team-a", ""), "request": types.NullValue})

	// assert
	require.NoError(t, err)
	assert.Equal(t, false, out.Value())
}

// TestMatchConditionsCompileInKyvernoEnv compiles the generated conditions in the environment
// Kyverno builds for ValidatingPolicy exception match conditions, whose "request" is typed,
// unlike cel.DynType above.
func TestMatchConditionsCompileInKyvernoEnv(t *testing.T) {
	env := kyvernoExceptionEnv(t)
	for name, conds := range map[string][]admissionregistrationv1.MatchCondition{
		"targets":        translateTargetsToMatchConditions([]policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"default", "team-*"}, Names: []string{"a", "b-*", "c?d"}}}, false),
		"namespaces":     translateTargetsToMatchConditions([]policyAPI.Target{{Kind: "Namespace", Namespaces: []string{"team-a", "team-*"}}}, false),
		"kind formats":   translateTargetsToMatchConditions([]policyAPI.Target{{Kind: "apps/v1/Deployment"}, {Kind: "v1/Pod"}, {Kind: "/v*/Pod"}, {Kind: "*"}, {Kind: "Deploy*"}}, false),
		"escaping":       translateTargetsToMatchConditions([]policyAPI.Target{{Kind: "Deployment", Namespaces: []string{`n"s\`}, Names: []string{`a"b\c`, `a"b\?`}}, {Kind: "Pod", Names: []string{`a"b\`}}}, false),
		"no targets":     translateTargetsToMatchConditions(nil, false),
		"chart-operator": chartOperatorMatchConditions([]string{"Namespace"}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, errs := kcompiler.CompileMatchConditions(field.NewPath("spec", "matchConditions"), env, conds...); len(errs) > 0 {
				t.Fatalf("kyverno rejected %q: %v", conds[0].Expression, errs.ToAggregate())
			}
		})
	}
}
