# SecurityGroup

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `SecurityGroup` · **Plural:** `securitygroups`

Firewall rules for a VPC: ingress and egress rules with protocol, ports, remote CIDR or remote security group.

## Spec

| Field                   | Type                                                                | Description                   |
| ----------------------- | ------------------------------------------------------------------- | ----------------------------- |
| `metadata`              | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels |
| `description`           | string                                                              | Optional description          |
| `vpcRef`                | [VPCRef](common-fields.md#vpcref)                                   | **Required**                  |
| `allowSameGroupTraffic` | `*bool`                                                             | Default `true`                |
| `ingressRules`          | `[]SecurityGroupRule`                                               | Ingress rules                 |
| `egressRules`           | `[]SecurityGroupRule`                                               | Egress rules                  |

### `SecurityGroupRule`

| Field                          | Description                                          |
| ------------------------------ | ---------------------------------------------------- |
| `name`                         | Optional rule name                                   |
| `protocol`                     | **Required** - e.g. `tcp`, `udp`, `icmp`, `all`      |
| `portRangeMin`, `portRangeMax` | Ignored for `all` / `icmp`                           |
| `remoteAddress`                | CIDR or IP                                           |
| `remoteSecurityGroupIdentity`  | Allow/deny traffic to/from another SG by Thalassa ID |
| `policy`                       | `allow` or `deny` (default `allow`)                  |
| `priority`                     | 1–199                                                |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_securitygroup.yaml`](../config/samples/iaas_v1_securitygroup.yaml)
