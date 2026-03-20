# iaas-controller

![Version: 1.0.0](https://img.shields.io/badge/Version-1.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 1.0.0](https://img.shields.io/badge/AppVersion-1.0.0-informational?style=flat-square)

Helm chart for deploying the Thalassa Cloud IaaS Controller.

## Source Code

* <https://github.com/thalassa-cloud/iaas-controller>

## Thalassa authentication

Set `thalassa.enabled: true` and configure **one** of the following (`authMethod`):

| `authMethod` | Description |
|--------------|-------------|
| `tokenExchange` | Federated workload identity: exchanges the pod’s Kubernetes service account JWT for a Thalassa API token via `POST` to `{url}/oidc/token`. Requires `thalassa.tokenExchange.serviceAccountId` (Thalassa service account ID). By default the chart mounts a **projected** service account token (`tokenExchange.projectedToken`) and sets `THALASSA_SUBJECT_TOKEN_FILE` to that path; set `projectedToken.enabled: false` and optionally `subjectTokenFile` to use the legacy automounted token path instead. |
| `pat` | Personal access token: mount from a Kubernetes `Secret` (`thalassa.pat.existingSecret` + `secretKey`) or set `thalassa.pat.token` for testing only. |
| `oidcClientCredentials` | OAuth2 client credentials: `thalassa.oidcClientCredentials.clientId` and client secret from `existingSecret` / `clientSecretKey`. |

Environment variables match `internal/iaas` (`THALASSA_*`). See also `BindThalassaViperEnv()` in the controller.

If `thalassa.enabled` is `false`, no `THALASSA_*` env vars are injected; supply credentials with `envFrom` (e.g. External Secrets Operator).

### Examples

**OIDC token exchange (in-cluster)**

```yaml
thalassa:
  enabled: true
  url: "https://api.thalassa.cloud/"
  organisation: "<org-id-or-slug>"
  authMethod: tokenExchange
  tokenExchange:
    serviceAccountId: "<thalassa-service-account-id>"
    # Default: projected SA token volume + THALASSA_SUBJECT_TOKEN_FILE. Optional:
    # projectedToken:
    #   audience: "https://api.thalassa.cloud/"  # set if your federated identity requires it
    # subjectTokenFile: "/var/run/secrets/..."   # overrides projected volume
```

When using token exchange, you need a federated identity. Bootstrap this for example through `tcloud`:

```bash
tcloud iam workload-identity-federation bootstrap kubernetes \
  --cluster <cluster-identity> \
  --namespace thalassa-iaas-controller \
  --service-account iaas-controller \
  --role iaas:FullAccess
```

The above bootstraps a federated identity for a newly created service account with the thalassa IAM rolebinding `iaas:FullAccess`, for the Kubernetes serviceaccount `thalassa-iaas-controller/iaas-controller`. Make sure that the namespace and serviceaccount match with the Iaas Controller deployment's.

**Personal access token**

```yaml
thalassa:
  enabled: true
  organisation: "<org-id-or-slug>"
  authMethod: pat
  pat:
    existingSecret: thalassa-api-token
    secretKey: token
```

Create the secret first: `kubectl create secret generic thalassa-api-token --from-literal=token=...`

**OIDC client credentials**

```yaml
thalassa:
  enabled: true
  organisation: "<org-id-or-slug>"
  authMethod: oidcClientCredentials
  oidcClientCredentials:
    clientId: "<client-id>"
    existingSecret: thalassa-oauth
    clientSecretKey: client-secret
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| enableServiceMonitor | bool | `true` |  |
| envFrom | list | `[]` |  |
| extraArgs | list | `[]` |  |
| fullnameOverride | string | `""` |  |
| hpa.cpu.averageUtilization | int | `90` |  |
| hpa.cpu.targetType | string | `"Utilization"` |  |
| hpa.enabled | bool | `false` |  |
| hpa.maxReplicas | int | `4` |  |
| hpa.minReplicas | int | `2` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"registry.thalassacloud.nl/thalassa-cloud/iaas-controller"` |  |
| image.tag | string | `nil` |  |
| imagePullSecrets | object | `{}` |  |
| minAvailable | int | `1` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.runAsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `65532` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| replicaCount | int | `1` |  |
| resources | object | `{}` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsUser | int | `65532` |  |
| service.type | string | `"ClusterIP"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |
| thalassa | object | see `values.yaml` | Thalassa API URL, organisation, and authentication |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
