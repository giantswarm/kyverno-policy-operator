[![CircleCI](https://dl.circleci.com/status-badge/img/gh/giantswarm/kyverno-policy-operator/tree/main.svg?style=svg)](https://dl.circleci.com/status-badge/redirect/gh/giantswarm/kyverno-policy-operator/tree/main)

# kyverno-policy-operator chart

Giant Swarm offers a kyverno-policy-operator App which can be installed in workload clusters.
Here we define the kyverno-policy-operator chart with its templates and default configuration.

Kyverno Policy Operator reconciles the Giant Swarm PolicyException instances and creates the necessary Kyverno PolicyExceptions objects.

A Giant Swarm PolicyException consists of a list of Policies and Targets to be excluded from the Kyverno Policy Engine. Having a Giant Swarm PolicyException will ensure that workloads targeted by that policy won't be blocked during Admission time by Kyverno.

### Sample Giant Swarm PolicyException:

In some cases, it may be necessary to exempt specific resources from the enforcement of Kyverno policies, such as `disallow-privilege-escalation` and `require-run-as-nonroot`. To achieve this, you can create a Giant Swarm PolicyException. Below is an example of how to exclude the `my-custom-operator` Deployment in the `default` namespace from these policies:

```yaml
apiVersion: policy.giantswarm.io/v1alpha1
kind: PolicyException
metadata:
  name: my-custom-operator
  namespace: policy-exceptions
spec:
  policies:
  - disallow-privilege-escalation
  - require-run-as-nonroot
  targets:
  - kind: Deployment
    names:
    - my-custom-operator
    namespaces:
    - default
```

This Policy Exception configuration will be detected by the Kyverno Policy Operator, which will create a corresponding Kyverno Policy Exception resource:

```yaml
apiVersion: kyverno.io/v2beta1
kind: PolicyException
metadata:
  labels:
    app.kubernetes.io/managed-by: kyverno-policy-operator
  name: my-custom-operator
  namespace: policy-exceptions
(...)
spec:
  background: false
  exceptions:
  - policyName: require-run-as-nonroot
    ruleNames:
    - run-as-non-root
    - autogen-run-as-non-root
    - autogen-cronjob-run-as-non-root
  - policyName: disallow-privilege-escalation
    ruleNames:
    - privilege-escalation
    - autogen-privilege-escalation
    - autogen-cronjob-privilege-escalation
  match:
    any:
    - resources:
        kinds:
        - Deployment
        - ReplicaSet
        - Pod
        names:
        - my-custom-operator*
        namespaces:
        - default
```

## CEL PolicyExceptions

Kyverno is moving policies from `kyverno.io/v1` ClusterPolicies to CEL-based Policy Types under
`policies.kyverno.io` (`ValidatingPolicy`, `ImageValidatingPolicy`, ...). To keep exceptions working
through that migration, the operator writes up to two Kyverno PolicyExceptions per Giant Swarm
PolicyException, both named after it:

- `kyverno.io/v2`, listing the policies that exist as ClusterPolicies, with their rules. Written only
  while `--legacy-exceptions` is enabled, and skipped (and deleted, if it exists) for a migrated
  gspolex (see below).
- `policies.kyverno.io/v1`, listing every policy. The policy's kind is resolved from the cluster
  (`ValidatingPolicy`, `MutatingPolicy` or `ImageValidatingPolicy`); when no matching CEL policy
  exists yet, it falls back to `ValidatingPolicy` and the name is recorded as unresolved.

When a ClusterPolicy is deleted, for example while it is being replaced, the `kyverno.io/v2`
PolicyException keeps its entry for that policy, with the rules it had, until the ClusterPolicy is
back. It is never deleted just because its ClusterPolicies are missing, only when legacy exceptions
are switched off, the gspolex is migrated or deleted, or none of its policies exists as a
ClusterPolicy or has an entry. The operator watches ClusterPolicies, so a new rule or `status.autogen` change is
picked up right away.

`--background-mode` (Helm `policyOperator.exceptionBackgroundMode`) only applies to the
`kyverno.io/v2` PolicyExceptions; `policies.kyverno.io/v1` PolicyExceptions have no such setting.

### How CEL exceptions match targets

The `policies.kyverno.io/v1` PolicyException turns the gspolex targets into one CEL match condition.
It matches more strictly than the `kyverno.io/v2` one, which keeps the old `name*` matching:

- **Names.** Objects of the target kind match a name exactly, or the `*`/`?` wildcard pattern the
  user wrote. `Pod`, `ReplicaSet` and `Job` targets use the legacy patterns instead, because these
  usually carry generated names: every name is cut to 58 characters and gets a trailing `*`.
- **Derived kinds.** The kinds a target's controller creates (ReplicaSet and Pod for a Deployment,
  Job and Pod for a CronJob, and Pod for any other kind but Pod) match the `<name>-` prefix, so a
  target `app-1` no longer covers the pods of `app-10`.
- **Namespaces.** A target's `namespaces` are compared with the object's namespace, and for a
  `Namespace` object with its name, as Kyverno does. A `Namespace` target with
  `namespaces: [team-a]` matches the Namespace `team-a`.
- **Kinds** use Kyverno's format. `Deployment` matches the kind in any API group,
  `apps/v1/Deployment` also checks `apiVersion`, `v1/Pod` matches version `v1` in any group, parts
  can be wildcards, `/v1/Pod` means the core group only, and `*` matches any kind. A wildcard group
  is only possible through the bare `Kind` or `version/Kind` forms: `apps/Deployment` and
  `*/v1/Deployment` parse as subresources and are left out. A kind with a subresource, such as
  `Pod/exec`, cannot be expressed in a CEL exception: that target is left out, and the operator
  logs it.
- **DELETE requests never match**, because a CEL exception sees a null `object` for them. When
  converting a legacy rule without `operations` to a CEL policy, list `CREATE` and `UPDATE`
  explicitly, so that DELETE requests are not validated.

The match conditions use CEL optional field syntax (`?field.orValue`), so they need Kubernetes 1.28
or newer wherever Kyverno turns ValidatingPolicies into ValidatingAdmissionPolicies.

### Migrated gspolexes

`exception-recommender` migrates legacy PolicyExceptions into gspolexes. A gspolex counts as
migrated only when it has both the `policy.giantswarm.io/migrated-from` annotation and the
`app.kubernetes.io/managed-by: exception-recommender` label. Its targets already list every kind the
legacy exception covered, so its CEL exception matches exactly those kinds and names, with no
derived kinds. It gets no `kyverno.io/v2` PolicyException.

### Labels and annotations

Both generated exceptions are labelled `app.kubernetes.io/managed-by: kyverno-policy-operator` and
`policy.giantswarm.io/source: gspolex|exception-recommender|chart-operator`. The operator only ever
deletes a PolicyException that carries the `managed-by` label; one created by hand or by another
tool is left alone. It also never changes a `policies.kyverno.io/v1` PolicyException without that
label: if one already has the generated name, the operator logs an error and counts it as
`reason="name_taken"` instead.

The generated `policies.kyverno.io/v1` PolicyException can also carry two annotations:

- `policy.giantswarm.io/migrated-from`: copied from the Giant Swarm PolicyException.
- `policy.giantswarm.io/unresolved-policies`: a comma-separated list of policy names that matched no
  CEL policy kind yet.

### chart-operator bypass

A separate `policies.kyverno.io/v1` PolicyException, `chart-operator-generated-sa-bypass` in the
`giantswarm` namespace, exempts chart-operator's CREATE and UPDATE of the configured
`policyOperator.chartOperatorExceptionKinds` from every `ValidatingPolicy` in the cluster, mirroring
the legacy ClusterPolicy bypass. The operator watches it, so a deleted or changed bypass is rebuilt
right away.

### `--legacy-exceptions`

The `--legacy-exceptions` flag (Helm `policyOperator.legacyExceptions`, default `true`) controls
whether `kyverno.io/v2` PolicyExceptions are written at all. The operator resolves it, together with
whether the `kyverno.io` ClusterPolicy and PolicyException CRDs are installed, into one of three
modes:

- `write`: the flag is enabled and both legacy CRDs exist. `kyverno.io/v2` PolicyExceptions are
  written as before.
- `cleanup`: both legacy CRDs exist but the flag is disabled. The per-gspolex `kyverno.io/v2`
  PolicyExceptions the operator manages are deleted, along with the legacy chart-operator bypass.
- `absent`: either legacy CRD is missing. The operator does not touch `kyverno.io/v2` at all and
  starts normally without them.

### Metrics

The operator exposes Prometheus metrics on the metrics endpoint (scraped by
`monitoring.podMonitor` when enabled):

- `kyverno_policy_operator_policyexceptions{api,source}`: generated PolicyExceptions by API
  (`legacy`/`cel`) and source (`gspolex`/`exception-recommender`/`chart-operator`).
- `kyverno_policy_operator_dual_policyexceptions{source}`: exceptions that exist as both a
  `kyverno.io/v2` and a `policies.kyverno.io` PolicyException.
- `kyverno_policy_operator_legacy_exceptions_enabled`: whether `kyverno.io/v2` PolicyExceptions are
  being written (`1`) or not (`0`).
- `kyverno_policy_operator_unresolved_policy_refs{policy}`: generated CEL exceptions referencing a
  policy name that currently matches no CEL policy.
- `kyverno_policy_operator_generation_errors_total{api,reason}`: errors writing or deleting generated
  PolicyExceptions (`reason`: `lookup_failed`, `apply_failed`, `delete_failed`, and for `cel` only,
  `name_taken`).

The gauges report `0` for every source they counted and found none of, and every error series
starts at `0`. The `legacy` and dual gauges are missing while the legacy CRDs are absent or the
legacy list fails, and `unresolved_policy_refs` only has series for names that are unresolved.

## Installing

There are several ways to install this app onto a workload cluster.

- [Using GitOps to instantiate the App](https://docs.giantswarm.io/advanced/gitops/apps/)
- [Using our web interface](https://docs.giantswarm.io/platform-overview/web-interface/app-platform/#installing-an-app).
- By creating an [App resource](https://docs.giantswarm.io/use-the-api/management-api/crd/apps.application.giantswarm.io/) in the management cluster as explained in [Getting started with App Platform](https://docs.giantswarm.io/getting-started/app-platform/).

## Configuring

### values.yaml

**This is an example of a values file you could upload using our web interface.**

```yaml
# Set the PolicyExceptions destination namespace
policyOperator:
  destinationNamespace: ""
```

### Sample App CR and ConfigMap for the management cluster

If you have access to the Kubernetes API on the management cluster, you could create
the App CR and ConfigMap directly.

Here is an example that would install the app to
workload cluster `abc12`:

```yaml
# appCR.yaml
apiVersion: application.giantswarm.io/v1alpha1
kind: App
metadata:
  name: kyverno-policy-operator
  namespace: demo01
spec:
  catalog: giantswarm-playground-test
  config:
    configMap:
      name: demo01-cluster-values
      namespace: demo01
  name: kyverno-policy-operator
  namespace: kyverno-policy-operator
  version: 0.0.1
```

```yaml
# user-values-configmap.yaml
policyOperator:
  destinationNamespace: "policy-exceptions"
```

See our [full reference on how to configure apps](https://docs.giantswarm.io/getting-started/app-platform/app-configuration/) for more details.

## Compatibility

This app has been tested to work with the following workload cluster release versions:

- v19.1.0

## Limitations

This App needs Kyverno App [v0.15+](https://github.com/giantswarm/kyverno-app) to be installed in the cluster.
