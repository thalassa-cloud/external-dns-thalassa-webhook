package thalassa

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIURL = "https://api.thalassa.cloud"
)

type Config struct {
	OrganisationIdentity string
	ProjectIdentity      string
	APIURL               string
	DomainFilter         []string
	DryRun               bool
	HTTPRetryMax         int
	HTTPRetryWaitMin     time.Duration
	HTTPRetryWaitMax     time.Duration
	Workers              int
	Insecure             bool

	PersonalToken       string
	TokenFile           string
	ClientID            string
	ClientSecret        string
	ClientSecretFile    string
	ServiceAccountID    string
	SubjectToken        string
	SubjectTokenFile    string
	OIDCTokenURL        string
	AccessTokenLifetime string
}

type FlagConfig struct {
	cfg          *Config
	domainFilter string
}

func DefaultConfig() *Config {
	return &Config{
		APIURL:           defaultAPIURL,
		HTTPRetryMax:     3,
		HTTPRetryWaitMin: 1 * time.Second,
		HTTPRetryWaitMax: 30 * time.Second,
		Workers:          10,
	}
}

// RegisterFlags binds Thalassa settings to fs. Flag defaults are seeded from the
// corresponding THALASSA_* environment variables so Kubernetes env-based deployment
// works without passing explicit flags. Call Parse on fs, then Config().
func RegisterFlags(fs *flag.FlagSet) *FlagConfig {
	cfg := DefaultConfig()
	fc := &FlagConfig{cfg: cfg}

	fs.StringVar(&cfg.APIURL, "thalassa-url", envStringDefault("THALASSA_API_URL", defaultAPIURL), "Thalassa Cloud API base URL")
	fs.StringVar(&cfg.OrganisationIdentity, "organisation", envString("THALASSA_ORGANISATION_ID"), "Thalassa organisation identity")
	fs.StringVar(&cfg.ProjectIdentity, "thalassa-project", envString("THALASSA_PROJECT_ID"), "Thalassa project identity")
	fs.StringVar(&cfg.PersonalToken, "thalassa-token", envString("THALASSA_TOKEN"), "Thalassa personal access token")
	fs.StringVar(&cfg.TokenFile, "thalassa-token-file", envString("THALASSA_TOKEN_FILE"), "Path to file containing a personal access token")
	fs.StringVar(&cfg.ClientID, "thalassa-client-id", envString("THALASSA_CLIENT_ID"), "OIDC client ID for client credentials flow")
	fs.StringVar(&cfg.ClientSecret, "thalassa-client-secret", envString("THALASSA_CLIENT_SECRET"), "OIDC client secret")
	fs.StringVar(&cfg.ClientSecretFile, "thalassa-client-secret-file", envString("THALASSA_CLIENT_SECRET_FILE"), "Path to file containing OIDC client secret")
	fs.StringVar(&cfg.ServiceAccountID, "thalassa-service-account-id", envString("THALASSA_SERVICE_ACCOUNT_ID"), "Thalassa service account identity for OIDC token exchange")
	fs.StringVar(&cfg.SubjectToken, "thalassa-subject-token", envString("THALASSA_SUBJECT_TOKEN"), "Subject JWT for OIDC token exchange")
	fs.StringVar(&cfg.SubjectTokenFile, "thalassa-subject-token-file", envString("THALASSA_SUBJECT_TOKEN_FILE"), "Path to file containing subject JWT for OIDC token exchange")
	fs.StringVar(&cfg.OIDCTokenURL, "thalassa-oidc-token-url", envString("THALASSA_OIDC_TOKEN_URL"), "OIDC token exchange URL")
	fs.StringVar(&cfg.AccessTokenLifetime, "thalassa-access-token-lifetime", envString("THALASSA_ACCESS_TOKEN_LIFETIME"), "Requested access token lifetime for OIDC token exchange")
	fs.BoolVar(&cfg.Insecure, "thalassa-insecure", envBool("THALASSA_INSECURE"), "Disable TLS certificate verification")
	fs.StringVar(&fc.domainFilter, "domain-filter", envString("THALASSA_DOMAIN_FILTER"), "Comma-separated list of DNS zones to manage")
	fs.BoolVar(&cfg.DryRun, "dry-run", envBool("THALASSA_DRY_RUN"), "Dry run mode")
	fs.IntVar(&cfg.HTTPRetryMax, "retry-max", envIntDefault("THALASSA_HTTP_RETRY_MAX", 3), "Maximum HTTP retries")
	fs.DurationVar(&cfg.HTTPRetryWaitMin, "retry-wait-min", envDurationDefault("THALASSA_HTTP_RETRY_WAIT_MIN", time.Second), "Minimum wait between retries")
	fs.DurationVar(&cfg.HTTPRetryWaitMax, "retry-wait-max", envDurationDefault("THALASSA_HTTP_RETRY_WAIT_MAX", 30*time.Second), "Maximum wait between retries")
	fs.IntVar(&cfg.Workers, "workers", envIntDefaultPositive("THALASSA_WORKERS", 10), "Concurrent zone workers when listing records")

	return fc
}

func (fc *FlagConfig) Config() (*Config, error) {
	if fc.domainFilter != "" {
		fc.cfg.DomainFilter = splitAndTrim(fc.domainFilter, ",")
	}
	if err := fc.cfg.Validate(); err != nil {
		return nil, err
	}
	return fc.cfg, nil
}

func (cfg *Config) Validate() error {
	if strings.TrimSpace(cfg.OrganisationIdentity) == "" {
		return fmt.Errorf("THALASSA_ORGANISATION_ID is required")
	}
	if authMethod(cfg) == "" {
		return fmt.Errorf("configure OIDC token exchange (THALASSA_SERVICE_ACCOUNT_ID + THALASSA_ORGANISATION_ID), THALASSA_TOKEN or THALASSA_TOKEN_FILE, or THALASSA_CLIENT_ID + THALASSA_CLIENT_SECRET or THALASSA_CLIENT_SECRET_FILE")
	}
	return nil
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envString(key string) string {
	return os.Getenv(key)
}

func envStringDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1"
}

func envIntDefault(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 0 {
		return defaultValue
	}
	return i
}

func envIntDefaultPositive(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(v)
	if err != nil || i <= 0 {
		return defaultValue
	}
	return i
}

func envDurationDefault(key string, defaultValue time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return defaultValue
	}
	return d
}
