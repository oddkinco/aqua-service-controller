# Contributing to Aqua Service Controller

Thank you for your interest in contributing to the Aqua Service Controller! This document provides guidelines and information for contributors.

## Getting Started

### Prerequisites

- Go 1.22 or later
- Docker (for building container images)
- kubectl (for testing against Kubernetes clusters)
- Access to AWS (for testing EBS operations)

### Setting Up Your Development Environment

1. **Clone the repository**
   ```bash
   git clone https://github.com/aqua-io/aqua-service-controller.git
   cd aqua-service-controller
   ```

2. **Install dependencies**
   ```bash
   go mod download
   ```

3. **Run tests**
   ```bash
   make test
   ```

4. **Build binaries**
   ```bash
   make build-all
   ```

## Development Workflow

### Making Changes

1. Create a new branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. Make your changes, following our coding standards

3. Write or update tests as needed

4. Run the test suite:
   ```bash
   make test
   make lint
   ```

5. Commit your changes with a descriptive message:
   ```bash
   git commit -m "feat: add support for multiple volume claim templates"
   ```

### Commit Message Format

We follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation changes
- `style:` - Code style changes (formatting, etc.)
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `ci:` - CI/CD changes
- `deps:` - Dependency updates

### Pull Requests

1. Push your branch to GitHub
2. Open a Pull Request against `main`
3. Fill out the PR template completely
4. Wait for CI checks to pass
5. Request review from maintainers

## Project Structure

```
aqua-service-controller/
├── api/v1alpha1/           # CRD type definitions
├── cmd/
│   ├── controller/         # Main controller binary
│   └── storagemover/       # CLI tool for testing
├── config/
│   ├── crd/               # CRD manifests
│   ├── rbac/              # RBAC configuration
│   ├── manager/           # Deployment manifests
│   └── samples/           # Example resources
├── internal/
│   ├── aws/               # AWS EBS operations
│   ├── controller/        # Reconciler logic
│   ├── migration/         # Core migration logic
│   └── multicluster/      # Multi-cluster client management
└── hack/                  # Development scripts
```

## Testing

### Unit Tests

Run unit tests with:
```bash
make test
```

With coverage:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration Tests

Integration tests require:
- Two Kubernetes clusters (source and destination)
- AWS credentials with EC2 permissions
- EBS volumes for testing

```bash
# Set up test environment
export SOURCE_KUBECONFIG=/path/to/source/kubeconfig
export DEST_KUBECONFIG=/path/to/dest/kubeconfig
export AWS_REGION=us-east-1

# Run integration tests
go test -tags=integration ./...
```

### Testing with the CLI

The `storagemover` CLI is useful for testing individual components:

```bash
# Build the CLI
make build-cli

# Inspect a PV
./bin/storagemover inspect-pv --source-kubeconfig=$KUBECONFIG --name=pvc-xxx

# Test PV translation
./bin/storagemover translate \
  --source-kubeconfig=$SOURCE_KUBECONFIG \
  --namespace=default \
  --name=data-web-0 \
  --dest-namespace=production

# Test volume detachment wait
./bin/storagemover wait-detach --volume-id=vol-xxx --aws-region=us-east-1
```

## Code Style

- Follow standard Go conventions
- Run `gofmt` and `goimports` before committing
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and small

## Documentation

- Update README.md for user-facing changes
- Add godoc comments to exported types and functions
- Update config/samples/ with new features
- Include migration guides for breaking changes

## Releasing

Releases are automated via GitHub Actions when a tag is pushed:

```bash
# Create a release tag
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

This will:
1. Run all tests
2. Build multi-platform Docker images
3. Push to ghcr.io
4. Create a GitHub release with binaries

## Getting Help

- Open an issue for bugs or feature requests
- Join our Slack channel: #aqua-service-controller
- Check existing issues and PRs before creating new ones

## Code of Conduct

Please be respectful and constructive in all interactions. We're all here to build great software together.
