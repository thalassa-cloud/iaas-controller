# Loadbalancer

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `Loadbalancer` · **Plural:** `loadbalancers`

Thalassa load balancer in a subnet, with listeners pointing at target groups and optional security groups.

## Spec

| Field                  | Type                                                                | Description                   |
| ---------------------- | ------------------------------------------------------------------- | ----------------------------- |
| `metadata`             | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels |
| `description`          | string                                                              | Optional                      |
| `subnetRef`            | [SubnetRef](common-fields.md#subnetref)                             | **Required**                  |
| `internalLoadbalancer` | bool                                                                | Internal-only (no public IP)  |
| `deleteProtection`     | bool                                                                | Block delete until cleared    |
| `listeners`            | `[]LoadbalancerListenerSpec`                                        | Listener definitions          |
| `securityGroupRefs`    | `[]` [SecurityGroupRef](common-fields.md#securitygroupref)          | Optional                      |

### `LoadbalancerListenerSpec`

| Field                   | Description                                                      |
| ----------------------- | ---------------------------------------------------------------- |
| `name`                  | **Required**                                                     |
| `description`           | Optional                                                         |
| `port`                  | **Required** (1–65535)                                           |
| `protocol`              | **Required** - `tcp` or `udp`                                    |
| `targetGroupRef`        | **Required** - [TargetGroupRef](common-fields.md#targetgroupref) |
| `maxConnections`        | Optional                                                         |
| `connectionIdleTimeout` | Optional (seconds)                                               |
| `allowedSources`        | Optional CIDR allow list                                         |

## Status

| Field                                        | Description                |
| -------------------------------------------- | -------------------------- |
| `resourceId`                                 | Thalassa identity          |
| `externalIpAddresses`, `internalIpAddresses` | VIPs                       |
| `hostname`                                   | DNS hostname if applicable |
| Reconcile + `conditions`                     | Standard                   |

## Example

[`config/samples/iaas_v1_loadbalancer.yaml`](../config/samples/iaas_v1_loadbalancer.yaml)
