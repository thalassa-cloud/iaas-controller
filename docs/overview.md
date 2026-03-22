# Overview

The iaas-controller reconciles each namespaced custom resource against the **Thalassa IaaS API**. Creating or updating a CR typically creates or updates the corresponding cloud resource; deleting the CR removes it from Thalassa (subject to finalizers and delete protection flags where applicable).

## Adoption and provisioning

Many resources support two modes:

1. **Provisioned by the controller** - you set the required spec fields (region, CIDRs, references to other CRs, etc.). The controller creates the resource in Thalassa and writes `status.resourceId`.
2. **Adopted by identity** - you set `spec.resourceId` (or equivalent) to an existing Thalassa resource. The controller **does not** create, update, or delete that cloud object; it syncs status from Thalassa only. See each resource doc for exact field names and mutual exclusions.

## Conditions and status

Status embeds **reconcile metadata** and **Kubernetes conditions** (`metav1.Condition`):

- **Ready** - high-level readiness for the object.
- **Available**, **Progressing**, **Degraded** - used for lifecycle and sync state.
- **Error** - reconciliation failures (often paired with `status.lastReconcileError`).

Common status fields (where present):

| Field                | Meaning                                    |
| -------------------- | ------------------------------------------ |
| `observedGeneration` | `metadata.generation` last reconciled      |
| `lastReconcileTime`  | Last reconcile attempt timestamp           |
| `lastReconcileError` | Last error message; cleared on success     |
| `resourceStatus`     | Provider-side status string (e.g. `ready`) |
| `resourceId`         | Thalassa identity of the managed object    |

## Suspend reconciliation

If the object has the annotation `iaas.controllers.thalassa.cloud/suspend` set to a truthy value (`true` or `1`), the controller skips reconciliation for that object.

## Events

The controller records Kubernetes **Events** on the CR (warnings on persisted errors, normal events on successful provisioning and deletion completion). Inspect with `kubectl describe <kind> <name>`.

## Dependencies

Resources that reference other CRs (for example Subnet → VPC) wait until the dependency exposes a `status.resourceId` before provisioning. Until then, the controller requeues without treating it as a hard error.
