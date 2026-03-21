# iaas-controller

Manage Thalassa Cloud resources from your Kubernetes cluster. Define VPCs, subnets, NAT gateways, route tables, security groups, target groups, and VPC peering connections as custom resources—the controller keeps them in sync with Thalassa so you can drive infrastructure declaratively with `kubectl` and GitOps.

## Description

The iaas-controller is a Kubernetes controller that extends Kubernetes with Custom Resource Definitions (CRDs) for Thalassa Cloud Infrastructure as a Service concepts. Users declare desired state (VPCs, subnets, NAT gateways, route tables and routes, security groups, target groups, and VPC peering connections) as Kubernetes resources; the controller reconciles them against the Thalassa IaaS API, creating, updating, and deleting cloud resources to match the cluster state. This gives teams a consistent, GitOps-friendly way to manage network and load-balancing infrastructure from within the cluster, with standard Kubernetes RBAC and tooling.

## Getting Started

### Prerequisites
- go version v1.24.6+
- kubectl version v1.35.0+.
- Access to a Kubernetes v1.35.0+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/iaas-controller:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/iaas-controller:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

## Deploy with Helm and workload identity federation

For production-style installs from the published OCI chart, the recommended authentication mode is **OIDC token exchange**: the controller uses the in-cluster Kubernetes service account token as a *subject token*, exchanges it at Thalassa’s token endpoint for an API access token, and calls the IaaS API with that token. That flow is set up with **workload identity federation** so Thalassa trusts tokens from your cluster’s service account.

### Prerequisites

- Thalassa `tcloud` CLI (or your org’s equivalent) with permission to run workload-identity bootstrap.
- Your **organisation** ID and **cluster** identity in Thalassa (from the console or your platform team).
- Helm 3.x and access to the chart registry (`oci://ghcr.io/thalassa-cloud/charts/…`).

### 1. Bootstrap federated identity

Before installing the controller, register the Kubernetes service account that the chart will use (default name `iaas-controller` in namespace `thalassa-iaas-controller`) with Thalassa IAM. The bootstrap command creates the federated binding and a Thalassa **service account** used for token exchange; you need its ID for Helm.

Example (adjust cluster ID, namespace, SA name, and role to match your environment):

```bash
export ORGANISATION_ID="<your-org-id>"
export CLUSTER_ID="<your-cluster-id>"

tcloud iam workload-identity-federation bootstrap kubernetes \
  --cluster "$CLUSTER_ID" \
  --namespace thalassa-iaas-controller \
  --service-account iaas-controller \
  --role iaas:FullAdminAccess
```

Copy **Thalassa service account ID** from the command output or UI (for example `sa-…`) into `THALASSA_SERVICE_ACCOUNT_ID` for the next step.

### 2. Helm values (token exchange)

Enable Thalassa, set your organisation, and configure `authMethod: tokenExchange` with the service account ID from bootstrap. The chart mounts a **projected** service account token and passes `--thalassa-subject-token-file` so the controller never relies on `THALASSA_*` environment variables for client configuration.

```yaml
thalassa:
  enabled: true
  url: "https://api.thalassa.cloud/"
  organisation: "<ORGANISATION_ID>"
  authMethod: tokenExchange
  tokenExchange:
    serviceAccountId: "<THALASSA_SERVICE_ACCOUNT_ID>"
    projectedToken:
      enabled: true
      audience: "https://api.thalassa.cloud/"

serviceAccount:
  create: true
```

You can merge this into a `values.yaml` file or set the same keys with `helm --set` (see below).

### 3. Install CRDs and controller

Install CRDs first, then the controller chart. Pin the chart version you intend to run (examples use a tag; replace with the current release).

```bash
export ORGANISATION_ID="<ORGANISATION_ID>"
export THALASSA_SERVICE_ACCOUNT_ID="<THALASSA_SERVICE_ACCOUNT_ID>"

helm upgrade --install iaas-controller-crds oci://ghcr.io/thalassa-cloud/charts/iaas-controller-crds:<version> \
  --namespace thalassa-iaas-controller \
  --create-namespace

helm upgrade --install iaas-controller oci://ghcr.io/thalassa-cloud/charts/iaas-controller:<version> \
  --namespace thalassa-iaas-controller \
  --create-namespace \
  --set thalassa.organisation="$ORGANISATION_ID" \
  --set thalassa.tokenExchange.serviceAccountId="$THALASSA_SERVICE_ACCOUNT_ID" \
  --set enableServiceMonitor=false
```

Other auth methods (personal access token, OAuth2 client credentials) and more chart options are documented in [`chart/iaas-controller/README.md`](chart/iaas-controller/README.md).

**Step-by-step copy/paste** (concrete namespace, chart versions, and optional `tcloud` profile notes) lives in [`deploy/README.md`](deploy/README.md). For **Flux CD**, see [`deploy/flux/README.md`](deploy/flux/README.md).

## To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/iaas-controller:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/iaas-controller/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
