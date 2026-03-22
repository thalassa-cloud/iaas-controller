# NatGateway

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `NatGateway` · **Plural:** `natgateways`

NAT gateway in a subnet, with optional security groups and default-route configuration.

## Spec

| Field                   | Type                                                                | Description                                          |
| ----------------------- | ------------------------------------------------------------------- | ---------------------------------------------------- |
| `metadata`              | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                        |
| `description`           | string                                                              | Optional description                                 |
| `subnetRef`             | [SubnetRef](common-fields.md#subnetref)                             | **Required** - subnet for the gateway                |
| `securityGroupRefs`     | `[]` [SecurityGroupRef](common-fields.md#securitygroupref)          | Optional attachments                                 |
| `configureDefaultRoute` | `*bool`                                                             | Default route in subnet route table (default `true`) |

## Status

| Field                                            | Description                 |
| ------------------------------------------------ | --------------------------- |
| `resourceId`                                     | Thalassa identity           |
| `endpointIP`                                     | Gateway endpoint / next hop |
| `v4IP`, `v6IP`                                   | Outbound addresses          |
| `resourceStatus`, reconcile fields, `conditions` | As usual                    |

## Example

[`config/samples/iaas_v1_natgateway.yaml`](../config/samples/iaas_v1_natgateway.yaml)
