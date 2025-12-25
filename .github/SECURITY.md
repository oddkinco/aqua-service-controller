# Security Policy

## Supported Versions

This project is currently in alpha development. Security updates will be applied to:

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability in the Aqua Service Controller, please report it responsibly.

### How to Report

Please report security vulnerabilities through one of these methods:

1. **GitHub Security Advisories** (Preferred): Use the [Security Advisories](../../security/advisories) feature to privately report a vulnerability.

2. **GitHub Issues**: For less sensitive security concerns, you can open an issue on the [GitHub Issues](../../issues) page with the `security` label.

When reporting, please include:
- A description of the vulnerability
- Steps to reproduce the issue
- Potential impact assessment
- Any suggested fixes (optional)

### What to Expect

- **Initial Response**: We aim to acknowledge reports within 48 hours.
- **Assessment**: We will provide an initial assessment as soon as possible.
- **Resolution**: We prioritize security fixes and aim to address critical vulnerabilities promptly.

### Disclosure Policy

- We follow coordinated disclosure practices
- We will credit reporters in the security advisory (unless you prefer anonymity)
- We request that you do not publicly disclose the vulnerability until we have released a fix

## Security Best Practices

When deploying the Aqua Service Controller:

### RBAC Configuration

- Use the minimum required permissions (see `config/rbac/role.yaml`)
- Create dedicated service accounts for the controller
- Regularly audit RBAC permissions

### Secrets Management

- Store kubeconfig secrets securely
- Use Kubernetes Secrets encryption at rest
- Consider using external secret management (e.g., AWS Secrets Manager, HashiCorp Vault)
- Rotate credentials regularly

### Network Security

- Deploy the controller in a dedicated namespace
- Use NetworkPolicies to restrict traffic
- Enable TLS for all cluster communications

### AWS Credentials

- Use IRSA (IAM Roles for Service Accounts) when running on EKS
- Follow the principle of least privilege for IAM policies
- Required permissions:
  ```json
  {
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": [
          "ec2:DescribeVolumes"
        ],
        "Resource": "*"
      }
    ]
  }
  ```

### Container Security

- Run containers as non-root (already configured)
- Use read-only root filesystem
- Set resource limits
- Keep images updated with security patches

## Security Scanning

This project uses automated security scanning:

- **govulncheck**: Go vulnerability database scanning
- **Trivy**: Container image vulnerability scanning
- **CodeQL**: Static analysis for security issues
- **Dependabot**: Automated dependency updates

## Changelog

Security-related changes are documented in our release notes with the `[SECURITY]` tag.
