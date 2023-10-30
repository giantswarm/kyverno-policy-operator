package controller

import (
	giantswarmPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2alpha1 "github.com/kyverno/kyverno/api/kyverno/v2alpha1"
)

// translateTargetsToResourceFilters takes a Giant Swarm PolicyException and creates the necessary Kyverno ResourceFilters
func translateTargetsToResourceFilters(polex giantswarmPolicy.PolicyException) kyvernov1.ResourceFilters {
	resourceFilters := kyvernov1.ResourceFilters{}
	for _, target := range polex.Spec.Targets {
		trasnlatedResourceFilter := kyvernov1.ResourceFilter{
			ResourceDescription: kyvernov1.ResourceDescription{
				Namespaces: target.Namespaces,
				Names:      target.Names,
				Kinds:      generateExceptionKinds(target.Kind),
			},
		}
		resourceFilters = append(resourceFilters, trasnlatedResourceFilter)
	}
	return resourceFilters
}

// translatePoliciesToExceptions takes a Kyverno ClusterPolicy array and transforms it into a Kyverno Exception array
func translatePoliciesToExceptions(policies map[string]kyvernov1.ClusterPolicy) []kyvernov2alpha1.Exception {
	var exceptionArray []kyvernov2alpha1.Exception
	for policyName, kyvernoPolicy := range policies {
		kyvernoException := kyvernov2alpha1.Exception{
			PolicyName: policyName,
			RuleNames:  generatePolicyRules(kyvernoPolicy),
		}
		exceptionArray = append(exceptionArray, kyvernoException)
	}

	return exceptionArray
}

// generatePolicyRules takes a Kyverno Policy name and generates a list of rules owned by that policy
func generatePolicyRules(kyvernoPolicy kyvernov1.ClusterPolicy) []string {
	var rulesArray []string
	for _, rule := range kyvernoPolicy.Spec.Rules {
		rulesArray = append(rulesArray, rule.Name)
	}
	for _, autogenRule := range kyvernoPolicy.Status.Autogen.Rules {
		rulesArray = append(rulesArray, autogenRule.Name)
	}

	return rulesArray
}

// unorderedEqual takes two Kyverno Exception arrays and checks if they are equal even if they are not ordered the same
func unorderedEqual(got, want []kyvernov2alpha1.Exception) bool {
	// Check Length size first
	if len(got) != len(want) {
		return false
	}
	// Create an exceptions map with the new desired Exceptions
	exceptionMap := make(map[string][]string)
	for _, exception := range want {
		exceptionMap[exception.PolicyName] = exception.RuleNames
	}
	for _, exception := range got {
		// Check if the Policy Name is still present in the new Exceptions
		if _, exists := exceptionMap[exception.PolicyName]; !exists {
			// The Policy is not present in the new array
			// Arrays are not equals, exit
			return false
		} else {
			// Check if the same RuleNames are still present in the new Exceptions
			for _, oldRule := range exception.RuleNames {
				found := false
				// Check against every rule, exit if found
				for _, newRule := range exceptionMap[exception.PolicyName] {
					if newRule == oldRule {
						// Found, break for
						found = true
						break
					}
				}
				if !found {
					// The arrays are not equals, exit
					return false
				}
				// Rules are equals, continue
			}
		}
	}
	// Arrays are equals
	return true
}
