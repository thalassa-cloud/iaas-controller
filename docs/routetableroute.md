# RouteTableRoute

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `RouteTableRoute` · **Plural:** `routetableroutes`

One route entry (destination + target) in a `RouteTable`.

## Spec

| Field                          | Type                                                                | Description                                                 |
| ------------------------------ | ------------------------------------------------------------------- | ----------------------------------------------------------- |
| `metadata`                     | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                               |
| `routeTableRef`                | [RouteTableRef](common-fields.md#routetableref)                     | **Required**                                                |
| `destinationCidrBlock`         | string                                                              | **Required** - e.g. `0.0.0.0/0`                             |
| `targetGatewayRef`             | `TargetGatewayRef`                                                  | Optional: `NatGateway` or `VpcPeeringConnection` by K8s ref |
| `targetNatGatewayId`           | string                                                              | Thalassa NAT gateway ID (if not using `targetGatewayRef`)   |
| `targetGatewayId`              | string                                                              | Thalassa gateway ID                                         |
| `targetVpcPeeringConnectionId` | `*string`                                                           | Thalassa peering connection ID                              |
| `gatewayAddress`               | string                                                              | Gateway IP for local/blackhole-style routes                 |

`targetGatewayRef` is mutually exclusive with the raw `target*` ID fields when resolving the next hop.

### `TargetGatewayRef`

| Field       | Description                            |
| ----------- | -------------------------------------- |
| `kind`      | `NatGateway` or `VpcPeeringConnection` |
| `name`      | Kubernetes resource name               |
| `namespace` | Optional                               |

## Status

`resourceId`, reconcile metadata, `conditions`.

## Example

[`config/samples/iaas_v1_routetableroute.yaml`](../config/samples/iaas_v1_routetableroute.yaml)
