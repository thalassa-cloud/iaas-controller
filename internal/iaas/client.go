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

package iaas

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	thalassaiaas "github.com/thalassa-cloud/client-go/iaas"
	"github.com/thalassa-cloud/client-go/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// thalassaLog is the logger for client setup (uses logf.Log; set ctrl.SetLogger before calling New* if you need output).
var thalassaLog = logf.Log.WithName("thalassa-client")

// DefaultKubernetesServiceAccountTokenPath is the default path for the projected or legacy
// Kubernetes service account JWT. Used as SubjectTokenFile for OIDC token exchange when no
// path is configured (federated workload identity in-cluster).
const DefaultKubernetesServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// defaultOIDCTokenURL returns {baseURL}/oidc/token with a single slash between host and path.
func defaultOIDCTokenURL(baseURL string) string {
	return strings.TrimSuffix(strings.TrimSpace(baseURL), "/") + "/oidc/token"
}

// NewFromEnv creates a Thalassa IaaS client using environment variables:
// THALASSA_BASE_URL, THALASSA_ORGANISATION, THALASSA_PERSONAL_ACCESS_TOKEN,
// or federated identity: THALASSA_SERVICE_ACCOUNT_ID with optional THALASSA_SUBJECT_TOKEN_FILE
// (defaults to the in-cluster service account token path).
func NewFromEnv() (*thalassaiaas.Client, error) {
	baseURL := os.Getenv("THALASSA_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("THALASSA_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.thalassa.cloud"
	}
	org := os.Getenv("THALASSA_ORGANISATION")
	if org == "" {
		org = os.Getenv("THALASSA_ORGANIZATION")
	}
	token := os.Getenv("THALASSA_PERSONAL_ACCESS_TOKEN")
	if token == "" {
		token = os.Getenv("THALASSA_TOKEN")
	}

	saID := strings.TrimSpace(os.Getenv("THALASSA_SERVICE_ACCOUNT_ID"))
	subjectFile := strings.TrimSpace(os.Getenv("THALASSA_SUBJECT_TOKEN_FILE"))
	subjectTok := strings.TrimSpace(os.Getenv("THALASSA_SUBJECT_TOKEN"))
	oidcTokenURL := strings.TrimSpace(os.Getenv("THALASSA_OIDC_TOKEN_URL"))
	accessLifetime := strings.TrimSpace(os.Getenv("THALASSA_ACCESS_TOKEN_LIFETIME"))

	opts := []client.Option{
		client.WithBaseURL(baseURL),
		client.WithOrganisation(org),
	}

	if saID != "" && org != "" {
		if subjectTok == "" && subjectFile == "" {
			subjectFile = DefaultKubernetesServiceAccountTokenPath
		}
		if oidcTokenURL == "" {
			oidcTokenURL = defaultOIDCTokenURL(baseURL)
		}
		subjectSource := subjectTokenSource(subjectTok, subjectFile)
		thalassaLog.Info("initializing Thalassa IaaS client from environment",
			"baseURL", baseURL,
			"organisation", org,
			"auth", "oidc-token-exchange",
			"serviceAccountId", saID,
			"oidcTokenURL", oidcTokenURL,
			"subjectTokenSource", subjectSource,
		)
		cfg := client.OIDCTokenExchangeConfig{
			TokenURL:            oidcTokenURL,
			SubjectToken:        subjectTok,
			SubjectTokenFile:    subjectFile,
			OrganisationID:      org,
			ServiceAccountID:    saID,
			AccessTokenLifetime: accessLifetime,
		}
		opts = append(opts, client.WithAuthOIDCTokenExchange(cfg))
	} else if token != "" {
		thalassaLog.Info("initializing Thalassa IaaS client from environment",
			"baseURL", baseURL,
			"organisation", org,
			"auth", "personal-access-token",
		)
		opts = append(opts, client.WithAuthPersonalToken(token))
	} else {
		err := fmt.Errorf("set THALASSA_SERVICE_ACCOUNT_ID and organisation for OIDC token exchange, or THALASSA_PERSONAL_ACCESS_TOKEN / THALASSA_TOKEN")
		thalassaLog.Error(err, "Thalassa client configuration invalid",
			"baseURL", baseURL, "organisationSet", org != "", "hasServiceAccountId", saID != "", "hasPersonalToken", token != "",
		)
		return nil, err
	}

	baseClient, err := client.NewClient(opts...)
	if err != nil {
		thalassaLog.Error(err, "failed to create Thalassa HTTP client", "baseURL", baseURL)
		return nil, err
	}
	iaasClient, err := thalassaiaas.New(baseClient)
	if err != nil {
		thalassaLog.Error(err, "failed to wrap Thalassa IaaS client", "baseURL", baseURL)
		return nil, err
	}
	thalassaLog.Info("Thalassa IaaS client ready", "baseURL", baseURL)
	return iaasClient, nil
}

// subjectTokenSource describes where the workload JWT comes from (no secret values).
func subjectTokenSource(subjectTok, subjectFile string) string {
	switch {
	case subjectTok != "":
		return "env"
	case subjectFile != "":
		return "file"
	default:
		return "unknown"
	}
}

// BindThalassaViperEnv binds environment variables for Thalassa settings used by NewClientFromEnv.
// Call once at process startup (e.g. after flag.Parse) before viper.Set for flags, or rely on
// NewClientFromEnv which invokes this as well.
func BindThalassaViperEnv() {
	_ = viper.BindEnv("organisation", "THALASSA_ORGANISATION", "THALASSA_ORGANIZATION")
	_ = viper.BindEnv("thalassa-url", "THALASSA_URL", "THALASSA_BASE_URL")
	_ = viper.BindEnv("thalassa-token", "THALASSA_TOKEN", "THALASSA_PERSONAL_ACCESS_TOKEN")
	_ = viper.BindEnv("thalassa-client-id", "THALASSA_CLIENT_ID")
	_ = viper.BindEnv("thalassa-client-secret", "THALASSA_CLIENT_SECRET")
	_ = viper.BindEnv("thalassa-project", "THALASSA_PROJECT")
	_ = viper.BindEnv("thalassa-region", "THALASSA_REGION")
	_ = viper.BindEnv("thalassa-insecure", "THALASSA_INSECURE")
	_ = viper.BindEnv("thalassa-service-account-id", "THALASSA_SERVICE_ACCOUNT_ID")
	_ = viper.BindEnv("thalassa-subject-token-file", "THALASSA_SUBJECT_TOKEN_FILE")
	_ = viper.BindEnv("thalassa-subject-token", "THALASSA_SUBJECT_TOKEN")
	_ = viper.BindEnv("thalassa-oidc-token-url", "THALASSA_OIDC_TOKEN_URL")
	_ = viper.BindEnv("thalassa-access-token-lifetime", "THALASSA_ACCESS_TOKEN_LIFETIME")
}

// NewClientFromEnv builds a Thalassa pkg/client using viper (and env via BindThalassaViperEnv).
//
// Authentication precedence:
//  1. OIDC token exchange when thalassa-service-account-id and organisation are set.
//     Subject JWT from thalassa-subject-token or thalassa-subject-token-file; if neither is set,
//     DefaultKubernetesServiceAccountTokenPath is used (in-cluster workload identity).
//  2. Personal access token (thalassa-token).
//  3. OIDC client credentials (thalassa-client-id + thalassa-client-secret).
func NewClientFromEnv() (client.Client, error) {
	BindThalassaViperEnv()

	baseURL := viper.GetString("thalassa-url")
	if baseURL == "" {
		baseURL = "https://api.thalassa.cloud"
	}
	org := strings.TrimSpace(viper.GetString("organisation"))
	project := viper.GetString("thalassa-project")
	personalAccessToken := viper.GetString("thalassa-token")
	clientID := viper.GetString("thalassa-client-id")
	clientSecret := viper.GetString("thalassa-client-secret")
	insecure := viper.GetBool("thalassa-insecure")

	serviceAccountID := strings.TrimSpace(viper.GetString("thalassa-service-account-id"))
	subjectTokenFile := strings.TrimSpace(viper.GetString("thalassa-subject-token-file"))
	subjectToken := strings.TrimSpace(viper.GetString("thalassa-subject-token"))
	oidcTokenURL := strings.TrimSpace(viper.GetString("thalassa-oidc-token-url"))
	accessTokenLifetime := strings.TrimSpace(viper.GetString("thalassa-access-token-lifetime"))

	tokenURL := defaultOIDCTokenURL(baseURL)

	opts := []client.Option{
		client.WithBaseURL(baseURL),
		client.WithOrganisation(org),
		client.WithUserAgent(fmt.Sprintf("iaas-controller/%s", "0.1.0")),
	}
	if project != "" {
		opts = append(opts, client.WithProject(project))
	}
	if insecure {
		opts = append(opts, client.WithInsecure())
	}

	switch {
	case serviceAccountID != "" && org != "":
		if subjectToken == "" && subjectTokenFile == "" {
			subjectTokenFile = DefaultKubernetesServiceAccountTokenPath
		}
		if oidcTokenURL == "" {
			oidcTokenURL = tokenURL
		}
		thalassaLog.Info("initializing Thalassa client (viper/env)",
			"baseURL", baseURL,
			"organisation", org,
			"auth", "oidc-token-exchange",
			"serviceAccountId", serviceAccountID,
			"oidcTokenURL", oidcTokenURL,
			"subjectTokenSource", subjectTokenSource(subjectToken, subjectTokenFile),
			"insecure", insecure,
			"projectSet", project != "",
		)
		cfg := client.OIDCTokenExchangeConfig{
			TokenURL:            oidcTokenURL,
			SubjectToken:        subjectToken,
			SubjectTokenFile:    subjectTokenFile,
			OrganisationID:      org,
			ServiceAccountID:    serviceAccountID,
			AccessTokenLifetime: accessTokenLifetime,
		}
		opts = append(opts, client.WithAuthOIDCTokenExchange(cfg))
	case personalAccessToken != "":
		thalassaLog.Info("initializing Thalassa client (viper/env)",
			"baseURL", baseURL,
			"organisation", org,
			"auth", "personal-access-token",
			"insecure", insecure,
			"projectSet", project != "",
		)
		opts = append(opts, client.WithAuthPersonalToken(personalAccessToken))
	case clientID != "" && clientSecret != "":
		thalassaLog.Info("initializing Thalassa client (viper/env)",
			"baseURL", baseURL,
			"organisation", org,
			"auth", "oidc-client-credentials",
			"oidcTokenURL", tokenURL,
			"insecure", insecure,
			"projectSet", project != "",
		)
		if insecure {
			opts = append(opts, client.WithAuthOIDCInsecure(clientID, clientSecret, tokenURL, insecure))
		} else {
			opts = append(opts, client.WithAuthOIDC(clientID, clientSecret, tokenURL))
		}
	default:
		err := fmt.Errorf("configure OIDC token exchange (thalassa-service-account-id + organisation), thalassa-token, or thalassa-client-id + thalassa-client-secret")
		thalassaLog.Error(err, "Thalassa client configuration invalid",
			"baseURL", baseURL,
			"organisationSet", org != "",
			"hasServiceAccountId", serviceAccountID != "",
			"hasPersonalToken", personalAccessToken != "",
			"hasClientCredentials", clientID != "" && clientSecret != "",
		)
		return nil, err
	}

	thalassaClient, err := client.NewClient(opts...)
	if err != nil {
		thalassaLog.Error(err, "failed to create Thalassa HTTP client", "baseURL", baseURL)
		return nil, fmt.Errorf("failed to create thalassa client: %w", err)
	}
	thalassaLog.Info("Thalassa HTTP client ready", "baseURL", baseURL)
	return thalassaClient, nil
}
