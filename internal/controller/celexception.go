package controller

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

const (
	TargetsMatchConditionName       = "gspolex-targets"
	ChartOperatorMatchConditionName = "chart-operator-sa"
	ChartOperatorUsername           = "system:serviceaccount:giantswarm:chart-operator"
)

// objectName falls back to generateName for objects that the API server names after admission,
// such as Pods created by a ReplicaSet.
const objectName = `(has(object.metadata.name) && object.metadata.name != "" ? object.metadata.name : (has(object.metadata.generateName) ? object.metadata.generateName : ""))`

const objectNamespace = `(has(object.metadata.namespace) ? object.metadata.namespace : "")`

// translateTargetsToMatchConditions turns gspolex targets into one CEL match condition that is true
// when any target matches. A null object (DELETE) never matches, and no targets match nothing:
// an exception without match conditions would exempt every resource.
func translateTargetsToMatchConditions(targets []policyAPI.Target) []admissionregistrationv1.MatchCondition {
	expression := "false"
	if len(targets) > 0 {
		parts := make([]string, 0, len(targets))
		for _, target := range targets {
			parts = append(parts, targetExpression(target))
		}
		expression = "object != null && (" + strings.Join(parts, " || ") + ")"
	}
	return []admissionregistrationv1.MatchCondition{{Name: TargetsMatchConditionName, Expression: expression}}
}

// targetExpression matches the target kind by its exact name (or the user's wildcard pattern), and
// the kinds its controller creates by "<name>-" prefix, because those carry generated names.
func targetExpression(target policyAPI.Target) string {
	kinds := generateExceptionKinds(target.Kind)
	own := fmt.Sprintf("(object.kind == %s && %s)", strconv.Quote(target.Kind), anyPattern(objectName, target.Names))
	clause := own
	if derived := kinds[1:]; len(derived) > 0 {
		clause = fmt.Sprintf("(%s || (object.kind in [%s] && %s))", own, quoteAll(derived), anyPattern(objectName, derivedPatterns(target.Names)))
	}
	return fmt.Sprintf("(%s && %s)", anyPattern(objectNamespace, target.Namespaces), clause)
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

// anyPattern is true when value matches any of the glob patterns, or always when there are none.
func anyPattern(value string, patterns []string) string {
	if len(patterns) == 0 {
		return "true"
	}
	parts := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?") {
			parts = append(parts, fmt.Sprintf("%s.matches(%s)", value, strconv.Quote(globToRegex(pattern))))
		} else {
			parts = append(parts, fmt.Sprintf("%s == %s", value, strconv.Quote(pattern)))
		}
	}
	return "(" + strings.Join(parts, " || ") + ")"
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

// chartOperatorMatchConditions exempts chart-operator's CREATE and UPDATE of the given kinds.
// Background scans have no request, so request is checked for null before it is read.
func chartOperatorMatchConditions(kinds []string) []admissionregistrationv1.MatchCondition {
	return []admissionregistrationv1.MatchCondition{{
		Name: ChartOperatorMatchConditionName,
		Expression: fmt.Sprintf(
			`request != null && has(request.userInfo) && has(request.userInfo.username) && request.userInfo.username == %s && request.operation in ["CREATE", "UPDATE"] && object != null && object.kind in [%s]`,
			strconv.Quote(ChartOperatorUsername), quoteAll(kinds)),
	}}
}
