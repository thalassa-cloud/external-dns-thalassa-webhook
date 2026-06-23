package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

type mockProvider struct {
	records      []*endpoint.Endpoint
	domainFilter endpoint.DomainFilter
	applyErr     error
	adjustErr    error
}

func (m *mockProvider) Records(_ context.Context) ([]*endpoint.Endpoint, error) {
	return m.records, nil
}

func (m *mockProvider) ApplyChanges(_ context.Context, _ *plan.Changes) error {
	return m.applyErr
}

func (m *mockProvider) AdjustEndpoints(endpoints []*endpoint.Endpoint) ([]*endpoint.Endpoint, error) {
	if m.adjustErr != nil {
		return nil, m.adjustErr
	}
	return endpoints, nil
}

func (m *mockProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return &m.domainFilter
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "0.0.0.0", cfg.Host)
	assert.Equal(t, 8888, cfg.Port)
	assert.NotZero(t, cfg.ReadTimeout)
	assert.NotZero(t, cfg.WriteTimeout)
}

func TestNewServer(t *testing.T) {
	provider := &mockProvider{}

	srv := New(provider, nil)
	assert.NotNil(t, srv)
	assert.Equal(t, 8888, srv.config.Port)

	cfg := &Config{Host: "127.0.0.1", Port: 9999}
	srv = New(provider, cfg)
	assert.Equal(t, 9999, srv.config.Port)
}

func TestHealthHandler(t *testing.T) {
	srv := New(&mockProvider{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.healthHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestNegotiateHandler(t *testing.T) {
	filter := endpoint.NewDomainFilter([]string{"example.com"})
	srv := New(&mockProvider{domainFilter: *filter}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	srv.negotiateHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, mediaTypeVersion, rr.Header().Get(contentTypeHeader))
}

func TestRecordsHandler_Get(t *testing.T) {
	records := []*endpoint.Endpoint{
		endpoint.NewEndpoint("test.example.com", endpoint.RecordTypeA, "1.2.3.4"),
	}
	srv := New(&mockProvider{records: records}, nil)

	req := httptest.NewRequest(http.MethodGet, "/records", nil)
	rr := httptest.NewRecorder()

	srv.recordsHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, mediaTypeVersion, rr.Header().Get(contentTypeHeader))

	var result []*endpoint.Endpoint
	err := json.NewDecoder(rr.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "test.example.com", result[0].DNSName)
}

func TestRecordsHandler_Post(t *testing.T) {
	srv := New(&mockProvider{}, nil)

	changes := plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("new.example.com", endpoint.RecordTypeA, "1.2.3.4"),
		},
	}

	body, err := json.Marshal(changes)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.recordsHandler(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

type mockSoftError struct{}

func (e *mockSoftError) Error() string   { return "soft error" }
func (e *mockSoftError) SoftError() bool { return true }

func TestRecordsHandler_SoftError(t *testing.T) {
	srv := New(&mockProvider{applyErr: &mockSoftError{}}, nil)

	body, _ := json.Marshal(plan.Changes{})

	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.recordsHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRecordsHandler_MethodNotAllowed(t *testing.T) {
	srv := New(&mockProvider{}, nil)

	req := httptest.NewRequest(http.MethodPut, "/records", nil)
	rr := httptest.NewRecorder()

	srv.recordsHandler(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestAdjustEndpointsHandler(t *testing.T) {
	srv := New(&mockProvider{}, nil)

	endpoints := []*endpoint.Endpoint{
		endpoint.NewEndpoint("test.example.com", endpoint.RecordTypeA, "1.2.3.4"),
	}

	body, err := json.Marshal(endpoints)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/adjustendpoints", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.adjustEndpointsHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, mediaTypeVersion, rr.Header().Get(contentTypeHeader))
}
