# VPC

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `VPC` · **Plural:** `vpcs`

Represents a Thalassa VPC. Either the controller **creates** the VPC from `region` and `cidrBlocks`, or the CR **adopts** an existing VPC by `spec.resourceId`.

## Spec

| Field         | Type                                                                 | Description                                                                  |
| ------------- | -------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `metadata`    | [Resource metadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                                |
| `description` | string                                                               | Optional description                                                         |
| `resourceId`  | string                                                               | Thalassa VPC identity for adoption only; no create/update/delete in Thalassa |
| `region`      | string                                                               | Region identity (required if not adopting)                                   |
| `cidrBlocks`  | `[]string`                                                           | VPC CIDRs (required if not adopting)                                         |

`resourceId` is mutually exclusive with provisioning: when set, `region` and `cidrBlocks` are not used for lifecycle.

## Status

| Field                                                           | Description                              |
| --------------------------------------------------------------- | ---------------------------------------- |
| `resourceId`                                                    | Thalassa identity                        |
| `resourceStatus`                                                | Provider status                          |
| `observedGeneration`, `lastReconcileTime`, `lastReconcileError` | Reconcile metadata                       |
| `conditions`                                                    | Standard conditions (Ready, Error, etc.) |

## Example

- [`config/samples/iaas_v1_vpc.yaml`](../config/samples/iaas_v1_vpc.yaml)
- [`config/samples/iaas_v1_vpc_reference.yaml`](../config/samples/iaas_v1_vpc_reference.yaml) (adoption)
