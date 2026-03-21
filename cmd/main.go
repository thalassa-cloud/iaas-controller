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

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	thalassaiaas "github.com/thalassa-cloud/client-go/iaas"
	"github.com/thalassa-cloud/client-go/tfs"
	iaasv1 "github.com/thalassa-cloud/iaas-controller/api/v1"
	"github.com/thalassa-cloud/iaas-controller/internal/controller"
	"github.com/thalassa-cloud/iaas-controller/internal/iaas"
	// +kubebuilder:scaffold:imports
)

const iaasClientEnvHint = "unable to create IaaS client; set organisation and one of: " +
	"thalassa-service-account-id (OIDC token exchange, uses in-cluster SA token by default), " +
	"thalassa-token, or thalassa-client-id + thalassa-client-secret"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(iaasv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")

	// Thalassa flags (bound to viper after Parse so iaas client can read them)
	var thalassaToken, thalassaClientID, thalassaClientSecret, thalassaURL, thalassaRegion, organisation string
	var thalassaServiceAccountID, thalassaSubjectTokenFile, thalassaSubjectToken string
	var thalassaOIDCTokenURL, thalassaAccessTokenLifetime string
	var thalassaInsecure bool
	flag.StringVar(&thalassaToken, "thalassa-token", "", "Thalassa Cloud access token")
	flag.StringVar(&thalassaClientID, "thalassa-client-id", "", "Thalassa Cloud client ID")
	flag.StringVar(&thalassaClientSecret, "thalassa-client-secret", "", "Thalassa Cloud client secret")
	flag.BoolVar(&thalassaInsecure, "thalassa-insecure", false, "Use insecure connection to Thalassa Cloud API")
	flag.StringVar(&thalassaURL, "thalassa-url", "https://api.thalassa.cloud/", "Thalassa Cloud API URL")
	flag.StringVar(&thalassaRegion, "thalassa-region", "", "Thalassa Cloud region slug or identity")
	flag.StringVar(&organisation, "organisation", "", "Thalassa Cloud organisation ID or Slug")
	flag.StringVar(&thalassaServiceAccountID, "thalassa-service-account-id", "",
		"Thalassa service account ID for OIDC token exchange (federated workload identity); "+
			"uses Kubernetes SA token file by default")
	flag.StringVar(&thalassaSubjectTokenFile, "thalassa-subject-token-file", "",
		"Path to subject JWT for token exchange (default: in-cluster service account token path when unset)")
	flag.StringVar(&thalassaSubjectToken, "thalassa-subject-token", "",
		"Inline subject JWT for token exchange (alternative to subject token file)")
	flag.StringVar(&thalassaOIDCTokenURL, "thalassa-oidc-token-url", "",
		"OIDC token endpoint (default: {thalassa-url}/oidc/token)")
	flag.StringVar(&thalassaAccessTokenLifetime, "thalassa-access-token-lifetime", "",
		"Optional exchanged access token lifetime (e.g. 39600s)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	iaas.BindThalassaViperEnv()

	// Bind Thalassa flag values to viper so internal/iaas client can read them
	if thalassaToken != "" {
		viper.Set("thalassa-token", thalassaToken)
	}
	if thalassaClientID != "" {
		viper.Set("thalassa-client-id", thalassaClientID)
	}
	if thalassaClientSecret != "" {
		viper.Set("thalassa-client-secret", thalassaClientSecret)
	}
	viper.Set("thalassa-insecure", thalassaInsecure)
	viper.Set("thalassa-url", thalassaURL)
	if thalassaRegion != "" {
		viper.Set("thalassa-region", thalassaRegion)
	}
	if organisation != "" {
		viper.Set("organisation", organisation)
	}
	if thalassaServiceAccountID != "" {
		viper.Set("thalassa-service-account-id", thalassaServiceAccountID)
	}
	if thalassaSubjectTokenFile != "" {
		viper.Set("thalassa-subject-token-file", thalassaSubjectTokenFile)
	}
	if thalassaSubjectToken != "" {
		viper.Set("thalassa-subject-token", thalassaSubjectToken)
	}
	if thalassaOIDCTokenURL != "" {
		viper.Set("thalassa-oidc-token-url", thalassaOIDCTokenURL)
	}
	if thalassaAccessTokenLifetime != "" {
		viper.Set("thalassa-access-token-lifetime", thalassaAccessTokenLifetime)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "iaas-controller.k8s.thalassa.cloud",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	thalassaClient, err := iaas.NewClientFromEnv()
	if err != nil {
		setupLog.Error(err, iaasClientEnvHint)
		os.Exit(1)
	}
	iaasClient, err := thalassaiaas.New(thalassaClient)
	if err != nil {
		setupLog.Error(err, "unable to create Thalassa IaaS client")
		os.Exit(1)
	}
	tfsClient, err := tfs.New(thalassaClient)
	if err != nil {
		setupLog.Error(err, "unable to create Thalassa TFS client")
		os.Exit(1)
	}

	if err := (&controller.VPCReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VPC")
		os.Exit(1)
	}
	if err := (&controller.SubnetReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Subnet")
		os.Exit(1)
	}
	if err := (&controller.SecurityGroupReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SecurityGroup")
		os.Exit(1)
	}
	if err := (&controller.NatGatewayReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NatGateway")
		os.Exit(1)
	}
	if err := (&controller.RouteTableReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RouteTable")
		os.Exit(1)
	}
	if err := (&controller.RouteTableRouteReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RouteTableRoute")
		os.Exit(1)
	}
	if err := (&controller.TargetGroupReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TargetGroup")
		os.Exit(1)
	}
	if err := (&controller.VpcPeeringConnectionReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VpcPeeringConnection")
		os.Exit(1)
	}
	if err := (&controller.LoadbalancerReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Loadbalancer")
		os.Exit(1)
	}
	if err := (&controller.BlockVolumeReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BlockVolume")
		os.Exit(1)
	}
	if err := (&controller.SnapshotReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Snapshot")
		os.Exit(1)
	}
	if err := (&controller.SnapshotPolicyReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SnapshotPolicy")
		os.Exit(1)
	}
	if err := (&controller.TfsInstanceReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		IaaSClient: iaasClient,
		TFSClient:  tfsClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TfsInstance")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
