# IaaS controller CRD reference

Documentation for the **Thalassa Cloud IaaS** custom resources managed by [iaas-controller](https://github.com/thalassa-cloud/iaas-controller).

## API

| Item            | Value                             |
| --------------- | --------------------------------- |
| **API group**   | `iaas.controllers.thalassa.cloud` |
| **API version** | `v1`                              |
| **Scope**       | Namespaced                        |

## Guides

- [Overview](overview.md) - reconciliation, conditions, adoption by `resourceId`
- [Common fields and references](common-fields.md) - `metadata` overrides, status, refs

## Resources

| Kind                 | Plural                  | Documentation                                   |
| -------------------- | ----------------------- | ----------------------------------------------- |
| VPC                  | `vpcs`                  | [VPC](vpc.md)                                   |
| Subnet               | `subnets`               | [Subnet](subnet.md)                             |
| NatGateway           | `natgateways`           | [NatGateway](natgateway.md)                     |
| SecurityGroup        | `securitygroups`        | [SecurityGroup](securitygroup.md)               |
| RouteTable           | `routetables`           | [RouteTable](routetable.md)                     |
| RouteTableRoute      | `routetableroutes`      | [RouteTableRoute](routetableroute.md)           |
| TargetGroup          | `targetgroups`          | [TargetGroup](targetgroup.md)                   |
| Loadbalancer         | `loadbalancers`         | [Loadbalancer](loadbalancer.md)                 |
| VpcPeeringConnection | `vpcpeeringconnections` | [VpcPeeringConnection](vpcpeeringconnection.md) |
| BlockVolume          | `blockvolumes`          | [BlockVolume](blockvolume.md)                   |
| Snapshot             | `snapshots`             | [Snapshot](snapshot.md)                         |
| SnapshotPolicy       | `snapshotpolicies`      | [SnapshotPolicy](snapshotpolicy.md)             |
| TfsInstance          | `tfsinstances`          | [TfsInstance](tfsinstance.md)                   |

## Source of truth

Field definitions and validation are generated from Go types under [`api/v1`](../api/v1/). OpenAPI schema is embedded in the CRDs under [`config/crd/bases`](../config/crd/bases/).

## Examples

Example manifests live under [`config/samples`](../config/samples/).
