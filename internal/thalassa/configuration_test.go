package thalassa

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thalassa-cloud/client-go/pkg/client"
)

func loadTestConfig(t *testing.T, args ...string) (*Config, error) {
	t.Helper()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fc := RegisterFlags(fs)
	require.NoError(t, fs.Parse(args))
	return fc.Config()
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, defaultAPIURL, cfg.APIURL)
	assert.Equal(t, 3, cfg.HTTPRetryMax)
	assert.Equal(t, 1*time.Second, cfg.HTTPRetryWaitMin)
	assert.Equal(t, 30*time.Second, cfg.HTTPRetryWaitMax)
	assert.Equal(t, 10, cfg.Workers)
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("THALASSA_TOKEN", "test-token")
	t.Setenv("THALASSA_ORGANISATION_ID", testOrg123)
	t.Setenv("THALASSA_PROJECT_ID", "proj-456")
	t.Setenv("THALASSA_API_URL", "https://api.example.com")
	t.Setenv("THALASSA_DOMAIN_FILTER", " example.com , test.org ")
	t.Setenv("THALASSA_DRY_RUN", "true")
	t.Setenv("THALASSA_HTTP_RETRY_MAX", "5")
	t.Setenv("THALASSA_HTTP_RETRY_WAIT_MIN", "2s")
	t.Setenv("THALASSA_HTTP_RETRY_WAIT_MAX", "45s")
	t.Setenv("THALASSA_WORKERS", "4")

	cfg, err := loadTestConfig(t)
	require.NoError(t, err)

	assert.Equal(t, "test-token", cfg.PersonalToken)
	assert.Equal(t, testOrg123, cfg.OrganisationIdentity)
	assert.Equal(t, "proj-456", cfg.ProjectIdentity)
	assert.Equal(t, testExampleAPIURL, cfg.APIURL)
	assert.Equal(t, []string{testExampleZone, "test.org"}, cfg.DomainFilter)
	assert.True(t, cfg.DryRun)
	assert.Equal(t, 5, cfg.HTTPRetryMax)
	assert.Equal(t, 2*time.Second, cfg.HTTPRetryWaitMin)
	assert.Equal(t, 45*time.Second, cfg.HTTPRetryWaitMax)
	assert.Equal(t, 4, cfg.Workers)
}

func TestLoadConfigFromEnv_OIDCTokenExchange(t *testing.T) {
	t.Setenv("THALASSA_TOKEN", "")
	t.Setenv("THALASSA_ORGANISATION_ID", testOrg123)
	t.Setenv("THALASSA_SERVICE_ACCOUNT_ID", "sa-456")

	cfg, err := loadTestConfig(t)
	require.NoError(t, err)
	assert.Equal(t, "sa-456", cfg.ServiceAccountID)
}

func TestLoadConfigFromFlags(t *testing.T) {
	t.Setenv("THALASSA_TOKEN", "env-token")
	t.Setenv("THALASSA_ORGANISATION_ID", "env-org")

	cfg, err := loadTestConfig(t,
		"--organisation=flag-org",
		"--thalassa-token=flag-token",
	)
	require.NoError(t, err)

	assert.Equal(t, "flag-org", cfg.OrganisationIdentity)
	assert.Equal(t, "flag-token", cfg.PersonalToken)
}

func TestLoadConfig_MissingAuth(t *testing.T) {
	t.Setenv("THALASSA_TOKEN", "")
	t.Setenv("THALASSA_ORGANISATION_ID", testOrg123)
	t.Setenv("THALASSA_SERVICE_ACCOUNT_ID", "")

	_, err := loadTestConfig(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configure OIDC token exchange")
}

func TestLoadConfig_MissingOrganisation(t *testing.T) {
	t.Setenv("THALASSA_TOKEN", "token")
	t.Setenv("THALASSA_ORGANISATION_ID", "")

	_, err := loadTestConfig(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "THALASSA_ORGANISATION_ID")
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: []string{}},
		{name: "single", input: testExampleZone, want: []string{testExampleZone}},
		{name: "multiple", input: " a.com , b.com ", want: []string{"a.com", "b.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitAndTrim(tt.input, ","))
		})
	}
}

func TestBuildClientOptions_AuthPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		cfg        Config
		wantErr    bool
		checkFirst func(t *testing.T, opts []client.Option)
	}{
		{
			name: "oidc token exchange",
			cfg: Config{
				OrganisationIdentity: testOrg1,
				ServiceAccountID:     "sa-1",
				APIURL:               testExampleAPIURL,
			},
			checkFirst: func(t *testing.T, opts []client.Option) {
				t.Helper()
				require.NotEmpty(t, opts)
			},
		},
		{
			name: "personal token",
			cfg: Config{
				OrganisationIdentity: testOrg1,
				PersonalToken:        "pat",
				APIURL:               testExampleAPIURL,
			},
		},
		{
			name: "client credentials",
			cfg: Config{
				OrganisationIdentity: testOrg1,
				ClientID:             "client",
				ClientSecret:         "secret",
				APIURL:               testExampleAPIURL,
			},
		},
		{
			name: "missing auth",
			cfg: Config{
				OrganisationIdentity: testOrg1,
				APIURL:               testExampleAPIURL,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := buildClientOptions(&tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, opts)
			if tt.checkFirst != nil {
				tt.checkFirst(t, opts)
			}
		})
	}
}

func TestBuildClientOptions_PersonalTokenFromFile(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token\n"), 0o600))

	cfg := &Config{
		OrganisationIdentity: testOrg1,
		TokenFile:            tokenFile,
		APIURL:               testExampleAPIURL,
	}

	opts, err := buildClientOptions(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestSubjectTokenSource(t *testing.T) {
	assert.Equal(t, "inline", subjectTokenSource("jwt", ""))
	assert.Equal(t, "file", subjectTokenSource("", "/var/run/token"))
	assert.Equal(t, "unknown", subjectTokenSource("", ""))
}

func TestAuthMethod(t *testing.T) {
	assert.Equal(t, "oidc-token-exchange", authMethod(&Config{
		OrganisationIdentity: testOrg,
		ServiceAccountID:     "sa",
	}))
	assert.Equal(t, "personal-access-token", authMethod(&Config{
		OrganisationIdentity: testOrg,
		PersonalToken:        "pat",
	}))
	assert.Equal(t, "oidc-client-credentials", authMethod(&Config{
		OrganisationIdentity: testOrg,
		ClientID:             "id",
		ClientSecret:         "secret",
	}))
	assert.Equal(t, "", authMethod(&Config{OrganisationIdentity: testOrg}))
}
