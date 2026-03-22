# Common fields and references

## Resource metadata (`spec.metadata`)

Optional overrides for how the object appears in Thalassa:

| Field    | Description                                                                |
| -------- | -------------------------------------------------------------------------- |
| `name`   | Overrides the Thalassa resource name (default: Kubernetes `metadata.name`) |
| `labels` | Labels applied in Thalassa                                                 |

Present on most CRs as `spec.metadata`.

## Reference patterns

Refs usually allow either a **Kubernetes name** (and optional `namespace`) or a **Thalassa identity** shortcut:

| Pattern                                | Behavior                                                            |
| -------------------------------------- | ------------------------------------------------------------------- |
| `identity` / `id` set                  | Controller uses that Thalassa ID directly                           |
| Only `name` (and optional `namespace`) | Controller resolves the referenced CR and reads `status.resourceId` |

Default namespace for refs is the **same namespace** as the referring object when `namespace` is omitted.

### `VPCRef`

- `name` (required for K8s resolution)
- `namespace` (optional)
- `identity` (optional Thalassa VPC ID)

### `SubnetRef`

- `name`, optional `namespace`, optional `identity`

### `SecurityGroupRef`

- `name`, optional `namespace`, optional `identity`

### `RouteTableRef`

- `name`, optional `namespace`, optional `identity`

### `TargetGroupRef`

- `name`, optional `namespace`, optional `identity`

### `VolumeRef` (snapshots, snapshot policies)

- `name`, optional `namespace`
- `id` - Thalassa volume identity when not using a `BlockVolume` CR

### `TargetGatewayRef` (route targets)

Used by `RouteTableRoute` when targeting a `NatGateway` or `VpcPeeringConnection` by Kubernetes reference:

- `kind`: `NatGateway` or `VpcPeeringConnection`
- `name`, optional `namespace`

Mutually exclusive with raw Thalassa ID fields on the same route spec; see [RouteTableRoute](routetableroute.md).
