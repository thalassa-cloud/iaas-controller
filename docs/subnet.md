# Subnet

**API:** `iaas.controllers.thalassa.cloud/v1`  
**Kind:** `Subnet` · **Plural:** `subnets`

Subnet inside a VPC. Provision with `vpcRef` + `cidr`, or adopt with `spec.resourceId`.

## Spec

| Field         | Type                                                                | Description                                                        |
| ------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------ |
| `metadata`    | [ResourceMetadata](common-fields.md#resource-metadata-specmetadata) | Optional Thalassa name/labels                                      |
| `description` | string                                                              | Optional description                                               |
| `resourceId`  | string                                                              | Adoption: existing subnet ID; controller does not manage lifecycle |
| `vpcRef`      | [VPCRef](common-fields.md#vpcref)                                   | Required when provisioning                                         |
| `cidr`        | string                                                              | Subnet CIDR (required when provisioning)                           |

## Status

| Field                    | Description                             |
| ------------------------ | --------------------------------------- |
| `resourceId`             | Thalassa identity                       |
| `resourceStatus`         | Provider status                         |
| Reconcile + `conditions` | Same pattern as [overview](overview.md) |

## Example

- [`config/samples/iaas_v1_subnet.yaml`](../config/samples/iaas_v1_subnet.yaml)
- [`config/samples/iaas_v1_subnet_reference.yaml`](../config/samples/iaas_v1_subnet_reference.yaml)
