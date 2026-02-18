# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add `io.giantswarm.application.audience` and `io.giantswarm.application.managed` chart annotations for Backstage visibility.
- Add PodLogs for log collection.
- Push to the `default` catalog.

### Changed

- Migrate chart metadata annotations to OCI-compatible format.

## [0.1.6] - 2025-10-21

### Changed

- Updated RBAC rules to include `policyexceptions/finalizers` for managing `.metadata.ownerReferences.blockOwnerDeletion`.
- Change CRD labels for easy adoption in the future.
- Remove PolicyManifest CRD.

## [0.1.5] - 2025-08-19

### Changed

- Resolve updated code linter findings.
- Update Kyverno API to v1.14.4.
- Gate PolicyManifest controller to avoid reconciliation when there is no PolMO running.

## [0.1.4] - 2025-01-31

### Changed

- Add validation before creating empty PolicyExceptions.

## [0.1.3] - 2025-01-29

### Changed

- Disable CRD install job and let them be installed by `policy-meta-operator`.

## [0.1.2] - 2025-01-28

### Changed

- Updated CRDs and fixed labels and annotations for `policy-meta-operator` deployment.

## [0.1.1] - 2025-01-20

### Changed

- Re enabled CRD install job.

## [0.1.0] - 2025-01-20

### Added

- Added the `policymanifest_controller`.

### Changed
- Disabled logger development mode to avoid panicking
- Changed the functions in `utils.go`
- Changed the `policyexception_controller` and `clusterpolicy_controller` to use `policy_api`.
- Disable PSPs.

## [0.0.7] - 2024-01-16

### Changed

- Update Kyverno `PolicyExceptions` to v2beta1

## [0.0.6] - 2023-12-11

### Changed

- Configure `gsoci.azurecr.io` as the default container image registry.

## [0.0.5] - 2023-11-10

### Changed

- Add `CiliumNetowrkPolicy`.

## [0.0.4] - 2023-10-31

### Changed

- Add `ClusterPolicy` controller to automatically exclude `chart-operator` ServiceAccount from custom policies.

## [0.0.3] - 2023-10-24

### Changed

- Set destination namespace to be `policy-exceptions`.

## [0.0.2] - 2023-10-10

### Changed

- Run preinstall job as non-root.

## [0.0.1] - 2023-10-05

- First release of the Kyverno Policy Operator App.

[Unreleased]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.7...v0.1.0
[0.0.7]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/giantswarm/kyverno-policy-operator/releases/tag/v0.0.1
