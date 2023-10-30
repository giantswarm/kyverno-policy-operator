# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.3...HEAD
[0.0.3]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/giantswarm/kyverno-policy-operator/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/giantswarm/kyverno-policy-operator/releases/tag/v0.0.1
