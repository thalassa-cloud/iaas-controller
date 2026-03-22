# SnapshotPolicy

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `SnapshotPolicy` · **Plural:** `snapshotpolicies`

Scheduled snapshot policy: cron schedule, timezone, TTL, optional keep count, and target volumes by label selector or explicit volume list.

## Spec

| Field         | Type                                                                | Description                                                  |
| ------------- | ------------------------------------------------------------------- | ------------------------------------------------------------ |
| `metadata`    | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                |
| `description` | string                                                              | Optional                                                     |
| `region`      | string                                                              | **Required** - volumes must be in this region                |
| `schedule`    | string                                                              | **Required** - cron expression                               |
| `timezone`    | string                                                              | **Required** - IANA timezone                                 |
| `ttl`         | `Duration`                                                          | **Required** - retention for snapshots created by the policy |
| `keepCount`   | `*int32`                                                            | Max snapshots to keep (oldest removed when exceeded)         |
| `enabled`     | bool                                                                | Whether the policy creates new snapshots                     |
| `target`      | `SnapshotPolicyTarget`                                              | **Required** - see below                                     |

### `SnapshotPolicyTarget`

| Field      | Description                                                      |
| ---------- | ---------------------------------------------------------------- |
| `type`     | **Required** - `selector` or `explicit`                          |
| `selector` | Label map when `type: selector`                                  |
| `volumes`  | List of [VolumeRef](snapshot.md#volumeref) when `type: explicit` |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_snapshotpolicy.yaml`](../config/samples/iaas_v1_snapshotpolicy.yaml)
