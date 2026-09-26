package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlanStartup(t *testing.T) {
	kinds := []string{"Namespace", "PolicyException"}
	tests := []struct {
		name            string
		mode            LegacyMode
		policyManifests bool
		kinds           []string
		want            Startup
	}{
		{
			name: "write", mode: LegacyWrite, kinds: kinds,
			want: Startup{LegacyWatches: true, ClusterPolicy: true, ChartOperatorBypass: true},
		},
		{
			name: "write with PolicyManifests", mode: LegacyWrite, policyManifests: true, kinds: kinds,
			want: Startup{LegacyWatches: true, ClusterPolicy: true, PolicyManifest: true, ChartOperatorBypass: true},
		},
		{
			name: "cleanup deletes the legacy bypass and watches nothing legacy", mode: LegacyCleanup, policyManifests: true, kinds: kinds,
			want: Startup{DeleteLegacyBypass: true, ChartOperatorBypass: true},
		},
		{
			name: "absent touches nothing legacy", mode: LegacyAbsent, policyManifests: true, kinds: kinds,
			want: Startup{ChartOperatorBypass: true},
		},
		{
			name: "no chart-operator kinds", mode: LegacyWrite,
			want: Startup{LegacyWatches: true, ClusterPolicy: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PlanStartup(tt.mode, tt.policyManifests, tt.kinds))
		})
	}
}
