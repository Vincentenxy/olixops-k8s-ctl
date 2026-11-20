# Unified JWT/PAT authentication implementation record

Date: 2026-11-19

## Scope

This change aligns the Publish to k8s-ctl authentication path.

- Publish sends the current request's JWT or PAT in `Authorization: Bearer <token>` for operator registration, capability checks, logs, terminal calls, commands and file transfers.
- k8s-ctl verifies RS256 JWTs with the platform public key and requires issuer, audience, expiry and `staffuserid`.
- k8s-ctl validates the signed PAT claims locally with the platform public key. PAT status, revocation, owner and business authorization remain Publish responsibilities.
- k8s-ctl has no Redis, Setting or PAT database dependency and remains independently deployable as a Kubernetes operator.
- The verified principal is stored in the k8s-ctl request context and is used by REST, MCP, terminal and Pod execution code.
- Publish's dedicated JWT signer and operator registration control-key file were removed.
- `auth.company`, local PAT private-key/database fields and other retired authentication fields are rejected by k8s-ctl configuration loading.
- The standalone Control API key remains only for the independent `auth.enabled=false` compatibility mode. Platform PAT issuance remains in Setting.

## Configuration

Platform operator configuration must provide at least one of JWT or PAT authentication.

JWT operator configuration must include:

- `auth.jwt.public-key`
- `auth.jwt.issuer`
- `auth.jwt.audience`

PAT configuration must include its own:

- `auth.pat.public-key`
- `auth.pat.issuer`
- `auth.pat.audience`
- `auth.pat.key-id`

The PAT public key is independent from the company JWT public key. k8s-ctl verifies PAT signatures locally and does not connect to Redis or Setting. Both public keys must be injected through a Secret-backed environment or another Secret-aware deployment mechanism. They must not be placed in the operator ConfigMap's `fileContent`.

The operator registration endpoint is an internal endpoint. Publish performs service, environment and workload authorization before registration. Network policy or an internal gateway must prevent developers from calling the operator directly and bypassing Publish's authorization boundary.

## Validation

k8s-ctl:

```text
GOROOT=/opt/homebrew/opt/go/libexec go test ./...
GOROOT=/opt/homebrew/opt/go/libexec go vet ./...
GOROOT=/opt/homebrew/opt/go/libexec go build ./...
```

All completed successfully.

Publish focused authentication and k8s-ctl integration tests:

```text
mvn -o -Dtest='Ctl*Test,ContainerHttpFlowTest,LogHttpFlowTest,ServiceObservationRuntimeTest,ServiceTargetResolverTest,CtlOperatorBootstrapTest,McpAuthenticationIntegrationTest' test
```

64 tests passed with 0 failures, 0 errors and 0 skipped.

Publish compilation and package creation:

```text
mvn -o -DskipTests package
```

Completed successfully.

Publish full test run executed `mvn -o test` with 2,506 tests. It reported 6 failures and 6 errors from the existing dirty workspace. The failures were in SQL knowledge row-count fixtures, RabbitMQ infrastructure, an existing Authz resource-path expectation and Authz preview. The errors were application-context bean replacement, existing inventory authorization test doubles and existing publish-plan authorization test doubles. The k8s-ctl focused authentication and Publish k8s-ctl tests passed independently. These full-suite failures remain recorded for the existing workspace and were not bypassed by changing production authentication logic.

## Review status

The implementation review checked authentication boundaries, credential propagation, configuration, error mapping and documentation. The remaining architectural prerequisite is network isolation of the operator registration endpoint. No separate registration credential was added because the accepted contract requires the current caller JWT/PAT to be used for registration and business requests.

The review also found that the outer authentication middleware protected `/api/v1/pub/ready`, which prevented Kubernetes readiness probes from succeeding without a Bearer header. Health and readiness are now both excluded from the authentication middleware, and the regression test covers both endpoints.

No production deployment, image push or Kubernetes change was performed.
