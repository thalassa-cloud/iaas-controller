# VpcPeeringConnection

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `VpcPeeringConnection` · **Plural:** `vpcpeeringconnections`

Peering between a **requester** VPC (Kubernetes `VPC` reference) and an **accepter** VPC identified by Thalassa IDs in another organisation. Supports `autoAccept`, and explicit `accept` / `reject` when the peering is pending.

## Spec

| Field                    | Type                                                                | Description                                                         |
| ------------------------ | ------------------------------------------------------------------- | ------------------------------------------------------------------- |
| `metadata`               | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                       |
| `description`            | string                                                              | Optional                                                            |
| `requesterVpcRef`        | [VPCRef](common-fields.md#vpcref)                                   | **Required** - local/requester VPC                                  |
| `accepterVpcId`          | string                                                              | **Required** - Thalassa ID of remote VPC                            |
| `accepterOrganisationId` | string                                                              | **Required** - Organisation owning accepter VPC                     |
| `autoAccept`             | bool                                                                | Auto-accept when allowed (same org/region constraints per provider) |
| `accept`                 | `*bool`                                                             | When true, controller calls Accept on pending peering               |
| `reject`                 | `*bool`                                                             | When true, controller calls Reject on pending peering               |
| `rejectReason`           | string                                                              | Optional reason when rejecting                                      |

## Status

| Field                    | Description                                    |
| ------------------------ | ---------------------------------------------- |
| `resourceId`             | Thalassa peering identity                      |
| `status`                 | Peering state (e.g. pending, active, rejected) |
| `resourceStatus`         | Mirrored provider status where applicable      |
| Reconcile + `conditions` | Standard                                       |

## Example

[`config/samples/iaas_v1_vpcpeeringconnection.yaml`](../config/samples/iaas_v1_vpcpeeringconnection.yaml)
