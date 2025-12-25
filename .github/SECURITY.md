# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.x.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability in the Aqua Service Controller, please report it responsibly.

### How to Report

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, please report them via one of these methods:

1. **GitHub Security Advisories**: Use the [Security Advisories](../../security/advisories) feature to privately report a vulnerability.

2. **Email**: Send an email to security@aqua.io with:
   - A description of the vulnerability
   - Steps to reproduce the issue
   - Potential impact assessment
   - Any suggested fixes (optional)

### What to Expect

- **Initial Response**: Within 48 hours, we will acknowledge receipt of your report.
- **Assessment**: Within 7 days, we will provide an initial assessment of the vulnerability.
- **Resolution Timeline**: We aim to release patches for critical vulnerabilities within 30 days.

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
