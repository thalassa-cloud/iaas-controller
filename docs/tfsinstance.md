# TfsInstance

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `TfsInstance` · **Plural:** `tfsinstances`

Thalassa TFS (Thalassa Filesystem Service) instance: sized storage in a VPC/subnet with optional security groups. The controller uses the Thalassa TFS API (separate from core IaaS) with the same credentials as the manager.

## Spec

| Field               | Type                                                                | Description                              |
| ------------------- | ------------------------------------------------------------------- | ---------------------------------------- |
| `metadata`          | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels            |
| `description`       | string                                                              | Optional                                 |
| `region`            | string                                                              | Optional; if empty, derived from the VPC |
| `vpcRef`            | [VPCRef](common-fields.md#vpcref)                                   | **Required**                             |
| `subnetRef`         | [SubnetRef](common-fields.md#subnetref)                             | **Required**                             |
| `sizeGB`            | int32                                                               | **Required** - size in GB (minimum 1)    |
| `securityGroupRefs` | `[]` [SecurityGroupRef](common-fields.md#securitygroupref)          | Optional                                 |
| `deleteProtection`  | bool                                                                | Prevent delete in Thalassa               |

## Status

`resourceId`, `resourceStatus`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_tfsinstance.yaml`](../config/samples/iaas_v1_tfsinstance.yaml)
