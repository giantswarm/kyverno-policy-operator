package controller

// Startup is what KPO runs for a legacy mode and its flags.
type Startup struct {
	// DeleteLegacyBypass deletes the kyverno.io/v2 chart-operator bypass before the controllers start.
	DeleteLegacyBypass bool
	// LegacyWatches makes the PolicyException controller own kyverno.io/v2 PolicyExceptions and watch
	// ClusterPolicies.
	LegacyWatches bool
	// ClusterPolicy runs the ClusterPolicy controller, which keeps the legacy chart-operator bypass.
	ClusterPolicy bool
	// PolicyManifest runs the PolicyManifest controller, which only writes kyverno.io/v2 PolicyExceptions.
	PolicyManifest bool
	// ChartOperatorBypass runs the controller that keeps the CEL chart-operator bypass.
	ChartOperatorBypass bool
}

// PlanStartup decides which controllers and watches run. Everything that writes or watches
// kyverno.io resources only runs while legacy exceptions are written.
func PlanStartup(mode LegacyMode, policyManifests bool, chartOperatorKinds []string) Startup {
	write := mode == LegacyWrite
	return Startup{
		DeleteLegacyBypass:  mode == LegacyCleanup,
		LegacyWatches:       write,
		ClusterPolicy:       write,
		PolicyManifest:      policyManifests && write,
		ChartOperatorBypass: len(chartOperatorKinds) > 0,
	}
}
