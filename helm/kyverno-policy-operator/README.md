# kyverno-policy-operator

A Helm chart for kyverno-policy-operator, which creates PolicyExceptions based on PolicyExceptionsDraft resources.

**Homepage:** <https://github.com/giantswarm/kyverno-policy-operator>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| name | string | `"kyverno-policy-operator"` |  |
| serviceType | string | `"managed"` |  |
| image.registry | string | `"gsoci.azurecr.io"` |  |
| image.name | string | `"giantswarm/kyverno-policy-operator"` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| global.image.registry | string | `"gsoci.azurecr.io"` |  |
| global.podSecurityStandards.enforced | bool | `true` |  |
| ciliumNetworkPolicy.enabled | bool | `true` |  |
| crds.install | bool | `false` |  |
| crds.image.tag | string | `"1.32.0"` |  |
| crds.resources.requests.cpu | string | `"100m"` |  |
| crds.resources.requests.memory | string | `"256Mi"` |  |
| crds.resources.limits.cpu | string | `"200m"` |  |
| crds.resources.limits.memory | string | `"512Mi"` |  |
| nodeSelector | object | `{}` |  |
| tolerations | list | `[]` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.runAsUser | int | `1000` |  |
| podSecurityContext.runAsGroup | int | `1000` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.readOnlyRootFilesystem | bool | `true` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| containerSecurityContext.allowPrivilegeEscalation | bool | `false` |  |
| containerSecurityContext.capabilities.drop[0] | string | `"ALL"` |  |
| containerSecurityContext.privileged | bool | `false` |  |
| containerSecurityContext.readOnlyRootFilesystem | bool | `true` |  |
| containerSecurityContext.runAsNonRoot | bool | `true` |  |
| containerSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"220Mi"` |  |
| resources.limits.cpu | string | `"100m"` |  |
| resources.limits.memory | string | `"220Mi"` |  |
| policyOperator.destinationNamespace | string | `"policy-exceptions"` |  |
| policyOperator.enablePolicyManifests | bool | `false` |  |
| policyOperator.exceptionBackgroundMode | bool | `true` |  |
| policyOperator.legacyExceptions | bool | `true` |  |
| policyOperator.chartOperatorExceptionKinds[0] | string | `"PolicyException"` |  |
| policyOperator.chartOperatorExceptionKinds[1] | string | `"Namespace"` |  |
| monitoring.podMonitor.enabled | bool | `true` |  |
| monitoring.podLogs.enabled | bool | `true` |  |
| monitoring.podLogs.tenant | string | `"giantswarm"` |  |
| monitoring.podLogs.annotations | object | `{}` |  |
| monitoring.podLogs.labels | object | `{}` |  |
| monitoring.podLogs.relabelings | list | `[]` |  |
