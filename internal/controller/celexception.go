package controller

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kubeutils "github.com/kyverno/kyverno/pkg/utils/kube"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

const (
	TargetsMatchConditionName       = "gspolex-targets"
	ChartOperatorMatchConditionName = "chart-operator-sa"
	ChartOperatorUsername           = "system:serviceaccount:giantswarm:chart-operator"
)

// objectName falls back to generateName for objects that the API server names after admission,
// such as Pods created by a ReplicaSet.
const objectName = `(object.metadata.?name.orValue("") != "" ? object.metadata.name : object.metadata.?generateName.orValue(""))`

// objectNamespace is a Namespace's own name, as in Kyverno's namespace matching for legacy exceptions.
const objectNamespace = `(object.kind == "Namespace" ? object.metadata.?name.orValue("") : object.metadata.?namespace.orValue(""))`

// translateTargetsToMatchConditions turns gspolex targets into one CEL match condition that is true
// when any target matches, with one target per line. A null object (DELETE) never matches, and no
// targets match nothing: an exception without match conditions would exempt every resource.
// Targets with a subresource kind are left out, see unsupportedTargetKinds. For a bridge, a gspolex
// migrated by exception-recommender, the targets already list every kind, so none are derived.
func translateTargetsToMatchConditions(targets []policyAPI.Target, bridge bool) []admissionregistrationv1.MatchCondition {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		if expression, ok := targetExpression(target, bridge); ok {
			parts = append(parts, expression)
		}
	}
	expression := "false"
	if len(parts) > 0 {
		expression = "object != null && (\n" + indent(strings.Join(parts, " ||\n")) + "\n)"
	}
	return []admissionregistrationv1.MatchCondition{{Name: TargetsMatchConditionName, Expression: expression}}
}

// unsupportedTargetKinds lists the target kinds a CEL exception cannot express: a subresource such
// as "Pod/exec", or an empty kind.
func unsupportedTargetKinds(targets []policyAPI.Target) []string {
	var kinds []string
	for _, target := range targets {
		if _, ok := targetExpression(target, false); !ok {
			kinds = append(kinds, target.Kind)
		}
	}
	return kinds
}

// targetExpression matches the target kind by its name (see ownPatterns), and, unless bridge is set,
// the kinds its controller creates by "<name>-" prefix, because those carry generated names. The kind uses Kyverno's format: "Kind", "version/Kind" or "group/version/Kind",
// each part may be a wildcard, and "*" is any kind. Missing namespaces or names match any. It
// reports false for a kind it cannot express.
func targetExpression(target policyAPI.Target, bridge bool) (string, bool) {
	group, version, kind, subresource := kubeutils.ParseKindSelector(target.Kind)
	if kind == "" || subresource != "" {
		return "", false
	}
	kinds := generateExceptionKinds(kind)
	namespace := anyPattern(objectNamespace, target.Namespaces)
	own := allOf(apiVersionPattern(group, version), kindPattern(kind), anyPattern(objectName, ownPatterns(kind, target.Names)))
	if bridge || len(kinds) == 1 {
		return "(" + allOf(namespace, own) + ")", true
	}
	derived := allOf(oneOf("object.kind", kinds[1:]), anyPattern(objectName, derivedPatterns(target.Names)))
	clause := "(\n  (" + own + ") ||\n  (" + derived + ")\n)"
	if namespace == "" {
		return clause, true
	}
	return "(" + namespace + " && " + clause + ")", true
}

// kindPattern matches object.kind, or any kind for "*".
func kindPattern(kind string) string {
	if kind == "*" {
		return ""
	}
	return anyPattern("object.kind", []string{kind})
}

// apiVersionPattern matches object.apiVersion for a Kyverno group and version. A "*" group also
// matches the core group, whose apiVersion has no group part.
func apiVersionPattern(group, version string) string {
	switch {
	case group == "*" && version == "*":
		return ""
	case group == "*":
		return anyPattern("object.apiVersion", []string{version, "*/" + version})
	case group == "":
		return anyPattern("object.apiVersion", []string{version})
	default:
		return anyPattern("object.apiVersion", []string{group + "/" + version})
	}
}

// ownPatterns returns the patterns a target's names match for the target kind itself: the exact
// name, or the user's wildcard pattern. Pods, ReplicaSets and Jobs usually carry generated names,
// so their names match by prefix, cut to 58 characters like the legacy "name*".
func ownPatterns(kind string, names []string) []string {
	if kind != KindPod && kind != KindReplicaSet && kind != KindJob {
		return names
	}
	patterns := make([]string, 0, len(names))
	for _, name := range names {
		if strings.ContainsAny(name, "*?") {
			patterns = append(patterns, name)
			continue
		}
		if len(name) > MaxNameLength {
			name = truncateName(name)
		}
		patterns = append(patterns, name+"*")
	}
	return patterns
}

// derivedPatterns turns target names into patterns for the objects their controllers create.
// Controllers name them "<name>-<suffix>" and the API server truncates generateName to 58
// characters, so the prefix is "<name>-" cut to 58. Unlike the legacy "name*", a target
// "test-app-1" does not cover pods of "test-app-10". Names the user wrote with a wildcard keep the
// legacy behaviour.
func derivedPatterns(names []string) []string {
	patterns := make([]string, 0, len(names))
	for _, name := range names {
		if strings.ContainsAny(name, "*?") {
			patterns = append(patterns, formatNames([]string{name})...)
			continue
		}
		prefix := name + "-"
		if len(prefix) > MaxNameLength {
			prefix = prefix[:MaxNameLength]
		}
		patterns = append(patterns, prefix+"*")
	}
	return patterns
}

// anyPattern is true when value matches any of the glob patterns, or "" (any value) when there are
// none. Exact values become == or in, a single trailing * becomes startsWith, and other globs an
// anchored regex.
func anyPattern(value string, patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	var exact, parts []string
	for _, pattern := range patterns {
		prefix, trailingStar := strings.CutSuffix(pattern, "*")
		switch {
		case !strings.ContainsAny(pattern, "*?"):
			exact = append(exact, pattern)
		case trailingStar && !strings.ContainsAny(prefix, "*?"):
			parts = append(parts, fmt.Sprintf("%s.startsWith(%s)", value, strconv.Quote(prefix)))
		default:
			parts = append(parts, fmt.Sprintf("%s.matches(%s)", value, strconv.Quote(globToRegex(pattern))))
		}
	}
	if len(exact) > 0 {
		parts = append([]string{oneOf(value, exact)}, parts...)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// oneOf is true when value equals one of the given values.
func oneOf(value string, values []string) string {
	if len(values) == 1 {
		return fmt.Sprintf("%s == %s", value, strconv.Quote(values[0]))
	}
	return fmt.Sprintf("%s in [%s]", value, quoteAll(values))
}

// allOf joins the non-empty clauses with &&, and is true when there are none.
func allOf(clauses ...string) string {
	nonEmpty := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		if clause != "" {
			nonEmpty = append(nonEmpty, clause)
		}
	}
	if len(nonEmpty) == 0 {
		return "true"
	}
	return strings.Join(nonEmpty, " && ")
}

// indent indents every line by two spaces.
func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// globToRegex converts a Kyverno-style wildcard (* and ?) into an anchored RE2 expression.
func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

func quoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	return strings.Join(quoted, ", ")
}

// chartOperatorMatchConditions exempts chart-operator's CREATE and UPDATE of the given kinds, with
// one check per line. Background scans have no request, so request is checked for null before it
// is read.
func chartOperatorMatchConditions(kinds []string) []admissionregistrationv1.MatchCondition {
	return []admissionregistrationv1.MatchCondition{{
		Name: ChartOperatorMatchConditionName,
		Expression: fmt.Sprintf(`request != null && has(request.userInfo) && has(request.userInfo.username) &&
request.userInfo.username == %s &&
request.operation in ["CREATE", "UPDATE"] &&
object != null && object.kind in [%s]`,
			strconv.Quote(ChartOperatorUsername), quoteAll(kinds)),
	}}
}
