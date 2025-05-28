# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Fix schema for Kyverno `PolicyExceptions`.

## [0.0.10] - 2025-05-28

### Changed

- Update `Kyverno` API from v2beta1 to v2.

## [0.0.9] - 2025-05-21

### Changed

- Modify `PolicyExceptions` CRD to allow for adoption by the `policy-api-crds` chart.

## [0.0.8] - 2024-08-29

### Changed

- Modify `PolicyExceptions` CRD to allow for easy adoption by `policy-meta-operator` chart.

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

[Unreleased]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.10...HEAD
[0.0.10]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.9...v0.0.10
[0.0.9]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.8...v0.0.9
[0.0.8]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.7...v0.0.8
[0.0.7]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/giantswarm/kyverno-policy-operator/releases/tag/v0.0.1
