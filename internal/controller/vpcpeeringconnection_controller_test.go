/*
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
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	iaasv1 "github.com/thalassa-cloud/iaas-controller/api/v1"
)

var _ = Describe("VpcPeeringConnection Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-vpcpeeringconnection"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		vpcpeeringconnection := &iaasv1.VpcPeeringConnection{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind VpcPeeringConnection")
			err := k8sClient.Get(ctx, typeNamespacedName, vpcpeeringconnection)
			if err != nil && errors.IsNotFound(err) {
				resource := &iaasv1.VpcPeeringConnection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: iaasv1.VpcPeeringConnectionSpec{
						RequesterVPCRef:        iaasv1.VPCRef{Name: "vpc-sample"},
						AccepterVpcId:          "accepter-vpc-id",
						AccepterOrganisationId: "accepter-org-id",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &iaasv1.VpcPeeringConnection{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance VpcPeeringConnection")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &VpcPeeringConnectionReconciler{
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				IaaSClient: nil,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
