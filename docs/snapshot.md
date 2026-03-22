# Snapshot

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `Snapshot` · **Plural:** `snapshots`

Point-in-time snapshot of a block volume.

## Spec

| Field              | Type                                                                | Description                   |
| ------------------ | ------------------------------------------------------------------- | ----------------------------- |
| `metadata`         | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels |
| `description`      | string                                                              | Optional                      |
| `volumeRef`        | `VolumeRef`                                                         | **Required** - see below      |
| `deleteProtection` | bool                                                                | Prevent delete in Thalassa    |

### VolumeRef

Reference a `BlockVolume` CR or a volume by Thalassa ID:

| Field       | Description                                      |
| ----------- | ------------------------------------------------ |
| `name`      | `BlockVolume` name                               |
| `namespace` | Optional                                         |
| `id`        | Thalassa volume identity (when not using `name`) |

Use either Kubernetes name resolution or `id`, per controller validation.

## Status

`resourceId`, `resourceStatus`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_snapshot.yaml`](../config/samples/iaas_v1_snapshot.yaml)
