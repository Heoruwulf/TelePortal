# Contributing to TelePortal

Thank you for your interest in contributing to TelePortal! As a high-performance audio bridge, we value contributions that improve performance, reliability, and usability.

## Code of Conduct

By participating in this project, you agree to maintain a professional and respectful environment.

## How Can I Contribute?

### Reporting Bugs

* Use the GitHub Issue tracker.
* Include a clear description, reproduction steps, and any relevant logs or environment details.

### Suggesting Enhancements

* Open an issue to discuss your idea before starting implementation.

### Pull Requests

1. **Fork the repository** and create your branch from `main`.
2. **Follow the Engineering Standards** defined in the project.
3. **Write Tests**: Ensure your changes are covered by unit tests.
4. **Pass QA**: Run `make qa` to ensure linting and type-checking pass.
5. **Update Documentation**: If you're adding a feature, update the relevant documentation in `docs/`.
6. **Submit the PR**: All PRs require approval from the Code Owner (@Heoruwulf).

## Engineering Principles

* **Standard Library First**: Minimize external dependencies.
* **Concurrency Safety**: Always handle goroutine lifecycles and context cancellation.
* **Performance**: Avoid unnecessary allocations; use buffer pools for hot paths.

Thank you for making TelePortal better!
