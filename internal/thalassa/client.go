package thalassa

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/thalassa-cloud/client-go/pkg/client"
)

// DefaultKubernetesServiceAccountTokenPath is the default path for the projected or legacy
// Kubernetes service account JWT. Used as SubjectTokenFile for OIDC token exchange when no
// path is configured (federated workload identity in-cluster).
const DefaultKubernetesServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

func defaultOIDCTokenURL(baseURL string) string {
	return strings.TrimSuffix(strings.TrimSpace(baseURL), "/") + "/oidc/token"
}

func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func buildClientOptions(cfg *Config) ([]client.Option, error) {
	baseURL := cfg.APIURL
	if baseURL == "" {
		baseURL = defaultAPIURL
	}

	org := strings.TrimSpace(cfg.OrganisationIdentity)
	project := strings.TrimSpace(cfg.ProjectIdentity)

	personalAccessToken := cfg.PersonalToken
	tokenFile := strings.TrimSpace(cfg.TokenFile)
	if tokenFile != "" {
		t, err := readSecretFile(tokenFile)
		if err != nil {
			return nil, err
		}
		personalAccessToken = t
	}

	clientID := cfg.ClientID
	clientSecret := cfg.ClientSecret
	clientSecretFile := strings.TrimSpace(cfg.ClientSecretFile)
	if clientSecretFile != "" {
		s, err := readSecretFile(clientSecretFile)
		if err != nil {
			return nil, err
		}
		clientSecret = s
	}

	serviceAccountID := strings.TrimSpace(cfg.ServiceAccountID)
	subjectTokenFile := strings.TrimSpace(cfg.SubjectTokenFile)
	subjectToken := strings.TrimSpace(cfg.SubjectToken)
	oidcTokenURL := strings.TrimSpace(cfg.OIDCTokenURL)
	accessTokenLifetime := strings.TrimSpace(cfg.AccessTokenLifetime)
	tokenURL := defaultOIDCTokenURL(baseURL)

	opts := []client.Option{
		client.WithBaseURL(baseURL),
		client.WithOrganisation(org),
		client.WithUserAgent("external-dns-thalassa-webhook"),
		client.WithRetries(cfg.HTTPRetryMax, cfg.HTTPRetryWaitMin, cfg.HTTPRetryWaitMax),
	}

	if project != "" {
		opts = append(opts, client.WithProject(project))
	}
	if cfg.Insecure {
		opts = append(opts, client.WithInsecure())
	}

	logger := slog.With(
		"baseURL", baseURL,
		"organisation", org,
		"insecure", cfg.Insecure,
		"projectSet", project != "",
	)

	switch {
	case serviceAccountID != "" && org != "":
		if subjectToken == "" && subjectTokenFile == "" {
			subjectTokenFile = DefaultKubernetesServiceAccountTokenPath
		}
		if oidcTokenURL == "" {
			oidcTokenURL = tokenURL
		}
		logger.With(
			"auth", "oidc-token-exchange",
			"serviceAccountId", serviceAccountID,
			"oidcTokenURL", oidcTokenURL,
			"subjectTokenSource", subjectTokenSource(subjectToken, subjectTokenFile),
		).Info("Initializing Thalassa client")

		opts = append(opts, client.WithAuthOIDCTokenExchange(client.OIDCTokenExchangeConfig{
			TokenURL:            oidcTokenURL,
			SubjectToken:        subjectToken,
			SubjectTokenFile:    subjectTokenFile,
			OrganisationID:      org,
			ServiceAccountID:    serviceAccountID,
			AccessTokenLifetime: accessTokenLifetime,
		}))
	case personalAccessToken != "":
		logger.With(
			"auth", "personal-access-token",
			"tokenFromFile", tokenFile != "",
		).Info("Initializing Thalassa client")
		opts = append(opts, client.WithAuthPersonalToken(personalAccessToken))
	case clientID != "" && clientSecret != "":
		logger.With(
			"auth", "oidc-client-credentials",
			"oidcTokenURL", tokenURL,
			"clientSecretFromFile", clientSecretFile != "",
		).Info("Initializing Thalassa client")
		if cfg.Insecure {
			opts = append(opts, client.WithAuthOIDCInsecure(clientID, clientSecret, tokenURL, cfg.Insecure))
		} else {
			opts = append(opts, client.WithAuthOIDC(clientID, clientSecret, tokenURL))
		}
	default:
		return nil, fmt.Errorf("configure OIDC token exchange (THALASSA_SERVICE_ACCOUNT_ID + THALASSA_ORGANISATION_ID), THALASSA_TOKEN or THALASSA_TOKEN_FILE, or THALASSA_CLIENT_ID + THALASSA_CLIENT_SECRET or THALASSA_CLIENT_SECRET_FILE")
	}

	return opts, nil
}

func subjectTokenSource(subjectTok, subjectFile string) string {
	switch {
	case subjectTok != "":
		return "inline"
	case subjectFile != "":
		return "file"
	default:
		return "unknown"
	}
}

func authMethod(cfg *Config) string {
	org := strings.TrimSpace(cfg.OrganisationIdentity)
	if strings.TrimSpace(cfg.ServiceAccountID) != "" && org != "" {
		return "oidc-token-exchange"
	}
	if cfg.PersonalToken != "" || strings.TrimSpace(cfg.TokenFile) != "" {
		return "personal-access-token"
	}
	if cfg.ClientID != "" && (cfg.ClientSecret != "" || strings.TrimSpace(cfg.ClientSecretFile) != "") {
		return "oidc-client-credentials"
	}
	return ""
}
