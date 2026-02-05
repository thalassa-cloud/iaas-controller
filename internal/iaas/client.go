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

	"github.com/spf13/viper"
	thalassaiaas "github.com/thalassa-cloud/client-go/iaas"
	"github.com/thalassa-cloud/client-go/pkg/client"
)

// NewFromEnv creates a Thalassa IaaS client using environment variables:
// THALASSA_BASE_URL, THALASSA_ORGANISATION, THALASSA_PERSONAL_ACCESS_TOKEN.
func NewFromEnv() (*thalassaiaas.Client, error) {
	baseURL := os.Getenv("THALASSA_BASE_URL")
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

	// TODO: OIDC authentication

	baseClient, err := client.NewClient(
		client.WithBaseURL(baseURL),
		client.WithOrganisation(org),
		client.WithAuthPersonalToken(token),
	)
	if err != nil {
		return nil, err
	}
	return thalassaiaas.New(baseClient)
}

func NewClientFromEnv() (client.Client, error) {
	baseURL := viper.GetString("thalassa-url")
	if baseURL == "" {
		baseURL = "https://api.thalassa.cloud"
	}
	org := viper.GetString("organisation")
	project := viper.GetString("thalassa-project")
	personalAccessToken := viper.GetString("thalassa-token")
	clientID := viper.GetString("thalassa-client-id")
	clientSecret := viper.GetString("thalassa-client-secret")
	insecure := viper.GetBool("thalassa-insecure")

	tokenURL := fmt.Sprintf("%s/oidc/token", baseURL)

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
	if personalAccessToken != "" {
		opts = append(opts, client.WithAuthPersonalToken(personalAccessToken))
	}
	if clientID != "" && clientSecret != "" {
		if insecure {
			opts = append(opts, client.WithAuthOIDCInsecure(clientID, clientSecret, tokenURL, insecure))
		} else {
			opts = append(opts, client.WithAuthOIDC(clientID, clientSecret, tokenURL))
		}
	}

	thalassaClient, err := client.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create thalassa client: %v", err)
	}
	return thalassaClient, nil
}
