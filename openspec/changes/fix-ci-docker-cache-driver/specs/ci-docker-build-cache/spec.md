## ADDED Requirements

### Requirement: CI Docker build uses docker-container driver
The CI pipeline SHALL configure a Docker Buildx builder with the `docker-container` driver before executing the Docker build step, so that GitHub Actions Cache (`type=gha`) can be used as a build cache backend.

#### Scenario: Docker build succeeds with GHA cache
- **WHEN** a push is made to the `main` branch triggering the CI workflow
- **THEN** the Docker Build & Push job SHALL complete successfully, using `cache-from: type=gha` and `cache-to: type=gha,mode=max` without error

#### Scenario: Cache export is supported
- **WHEN** the buildx builder is configured with `docker-container` driver
- **THEN** the `type=gha` cache export SHALL be supported and operational
