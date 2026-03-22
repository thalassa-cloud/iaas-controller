# TargetGroup

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `TargetGroup` · **Plural:** `targetgroups`

Load-balancer target group in a VPC: port, protocol (`tcp` / `udp`), optional label selector or explicit server attachments, health check, and balancing policy.

## Spec

| Field                 | Type                                                                | Description                                                        |
| --------------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------ |
| `metadata`            | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                      |
| `description`         | string                                                              | Optional                                                           |
| `vpcRef`              | [VPCRef](common-fields.md#vpcref)                                   | **Required**                                                       |
| `targetPort`          | int                                                                 | **Required** (1–65535)                                             |
| `protocol`            | string                                                              | **Required** - `tcp` or `udp`                                      |
| `targetSelector`      | `map[string]string`                                                 | Match servers by labels                                            |
| `attachments`         | `[]TargetGroupAttachment`                                           | Explicit `serverIdentity` / optional `endpointIdentity`            |
| `enableProxyProtocol` | `*bool`                                                             | Optional                                                           |
| `loadbalancingPolicy` | `*string`                                                           | e.g. `ROUND_ROBIN`, `RANDOM`, `MAGLEV`                             |
| `healthCheck`         | `HealthCheck`                                                       | Protocol (`TCP`/`HTTP`/`HTTPS`), port, path, intervals, thresholds |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_targetgroup.yaml`](../config/samples/iaas_v1_targetgroup.yaml)
