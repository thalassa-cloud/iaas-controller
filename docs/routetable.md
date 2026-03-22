# RouteTable

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `RouteTable` · **Plural:** `routetables`

A route table attached to a VPC. Individual routes are separate `RouteTableRoute` resources.

## Spec

| Field         | Type                                                                | Description                   |
| ------------- | ------------------------------------------------------------------- | ----------------------------- |
| `metadata`    | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels |
| `description` | string                                                              | Optional description          |
| `vpcRef`      | [VPCRef](common-fields.md#vpcref)                                   | **Required**                  |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_routetable.yaml`](../config/samples/iaas_v1_routetable.yaml)

See also [RouteTableRoute](routetableroute.md) for routes.
