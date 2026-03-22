# BlockVolume

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `BlockVolume` · **Plural:** `blockvolumes`

Thalassa block volume (disk) in a region with a given size and volume type.

## Spec

| Field                         | Type                                                                | Description                                                             |
| ----------------------------- | ------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `metadata`                    | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                           |
| `description`                 | string                                                              | Optional                                                                |
| `size`                        | int32                                                               | **Required** - size in GB (minimum 1)                                   |
| `region`                      | string                                                              | **Required** - Thalassa region identity or slug                         |
| `volumeTypeId`                | string                                                              | **Required** - volume type identity (controller may resolve name to ID) |
| `type`                        | string                                                              | Volume kind for API (e.g. `block`); optional                            |
| `restoreFromSnapshotIdentity` | `*string`                                                           | Create volume from snapshot; region must match                          |
| `deleteProtection`            | bool                                                                | Prevent delete in Thalassa                                              |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_blockvolume.yaml`](../config/samples/iaas_v1_blockvolume.yaml)
