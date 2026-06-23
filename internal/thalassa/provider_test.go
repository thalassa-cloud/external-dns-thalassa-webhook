package thalassa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thalassa-cloud/client-go/dns"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	extdnsprovider "sigs.k8s.io/external-dns/provider"
)

const (
	testExampleZone   = "example.com"
	testExampleAPIURL = "https://api.thalassa.cloud"
	testOrg1          = "org-1"
	testOrg           = "org"
	testOrg123        = "org-123"
	testZoneID1       = "dnsz-1"
)

type mockDNSClient struct {
	zones   []dns.DnsZone
	records map[string][]dns.DnsRecord
}

func (m *mockDNSClient) ListZones(_ context.Context, _ *dns.ListZonesRequest) ([]dns.DnsZone, error) {
	return m.zones, nil
}

func (m *mockDNSClient) ListRecords(_ context.Context, zoneIdentity string, _ *dns.ListRecordsRequest) ([]dns.DnsRecord, error) {
	for _, zone := range m.zones {
		if zone.Identity == zoneIdentity {
			return m.records[zone.Name], nil
		}
	}
	return nil, nil
}

func (m *mockDNSClient) CreateRecord(_ context.Context, _ string, create dns.CreateDnsRecordRequest) (*dns.DnsRecord, error) {
	return &dns.DnsRecord{
		Identity: "dnsr-new",
		Name:     create.Name,
		Type:     create.Type,
		TTL:      create.TTL,
		Values:   create.Values,
	}, nil
}

func (m *mockDNSClient) UpdateRecord(_ context.Context, _, recordIdentity string, update dns.UpdateDnsRecordRequest) (*dns.DnsRecord, error) {
	return &dns.DnsRecord{
		Identity: recordIdentity,
		Values:   update.Values,
		TTL:      update.TTL,
	}, nil
}

func (m *mockDNSClient) DeleteRecord(_ context.Context, _, recordIdentity string) error {
	return fmt.Errorf("failed to delete record %s", recordIdentity)
}

func newTestProvider(client dnsAPI, domains ...string) *Provider {
	return &Provider{
		client:       client,
		domainFilter: endpoint.NewDomainFilter(domains),
		workers:      2,
		log:          slog.With("component", "provider"),
	}
}

func TestProvider_Records(t *testing.T) {
	client := &mockDNSClient{
		zones: []dns.DnsZone{
			{Identity: testZoneID1, Name: testExampleZone},
			{Identity: "dnsz-2", Name: "other.com"},
		},
		records: map[string][]dns.DnsRecord{
			testExampleZone: {
				{Identity: "dnsr-1", Name: "www", Type: dns.DnsRecordTypeA, TTL: 300, Values: []string{"192.0.2.1"}},
				{Identity: "dnsr-2", Name: "@", Type: dns.DnsRecordTypeMX, TTL: 300, Values: []string{"10 mail.example.com"}},
			},
		},
	}

	provider := newTestProvider(client, testExampleZone)

	endpoints, err := provider.Records(context.Background())
	require.NoError(t, err)
	require.Len(t, endpoints, 2)

	assert.Equal(t, "www.example.com", endpoints[0].DNSName)
	assert.Equal(t, endpoint.RecordTypeA, endpoints[0].RecordType)
	assert.Equal(t, endpoint.Targets{"192.0.2.1"}, endpoints[0].Targets)

	assert.Equal(t, testExampleZone, endpoints[1].DNSName)
	assert.Equal(t, endpoint.RecordTypeMX, endpoints[1].RecordType)
	assert.Equal(t, endpoint.Targets{"10 mail.example.com"}, endpoints[1].Targets)
}

func TestProvider_ApplyChanges_Create(t *testing.T) {
	client := &mockDNSClient{
		zones: []dns.DnsZone{{Identity: testZoneID1, Name: testExampleZone}},
		records: map[string][]dns.DnsRecord{
			testExampleZone: {},
		},
	}

	provider := newTestProvider(client, testExampleZone)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpointWithTTL("app.example.com", endpoint.RecordTypeA, 600, "203.0.113.10"),
		},
	}

	err := provider.ApplyChanges(context.Background(), changes)
	require.NoError(t, err)
}

func TestProvider_ApplyChanges_DeleteSoftError(t *testing.T) {
	client := &mockDNSClient{
		zones: []dns.DnsZone{{Identity: testZoneID1, Name: testExampleZone}},
		records: map[string][]dns.DnsRecord{
			testExampleZone: {
				{Identity: "dnsr-1", Name: "app", Type: dns.DnsRecordTypeA, TTL: 300, Values: []string{"203.0.113.10"}},
			},
		},
	}

	provider := newTestProvider(client, testExampleZone)

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			endpoint.NewEndpoint("app.example.com", endpoint.RecordTypeA, "203.0.113.10"),
		},
	}

	err := provider.ApplyChanges(context.Background(), changes)
	require.Error(t, err)
	assert.True(t, errors.Is(err, extdnsprovider.SoftError))
}

func TestRecordNameFromEndpoint(t *testing.T) {
	tests := []struct {
		zoneName string
		dnsName  string
		want     string
	}{
		{zoneName: testExampleZone, dnsName: testExampleZone, want: "@"},
		{zoneName: testExampleZone, dnsName: "www.example.com", want: "www"},
	}

	for _, tt := range tests {
		t.Run(tt.dnsName, func(t *testing.T) {
			assert.Equal(t, tt.want, recordNameFromEndpoint(tt.zoneName, tt.dnsName))
		})
	}
}

func TestFormatRecordValue(t *testing.T) {
	assert.Equal(t, "203.0.113.1", formatRecordValue(endpoint.RecordTypeA, "203.0.113.1"))
	assert.Equal(t, "10 mail.example.com", formatRecordValue(endpoint.RecordTypeMX, "10 mail.example.com."))
	assert.Equal(t, "target.example.com", formatRecordValue(endpoint.RecordTypeCNAME, "target.example.com."))
}

func TestTargetMatchesValue(t *testing.T) {
	assert.True(t, targetMatchesValue(endpoint.RecordTypeA, "1.2.3.4", "1.2.3.4"))
	assert.True(t, targetMatchesValue(endpoint.RecordTypeCNAME, "target.example.com.", "target.example.com"))
	assert.True(t, targetMatchesValue(endpoint.RecordTypeMX, "10 mail.example.com.", "10 mail.example.com"))
}

func TestSupportedRecordType(t *testing.T) {
	provider := newTestProvider(&mockDNSClient{})

	assert.True(t, provider.SupportedRecordType(endpoint.RecordTypeA))
	assert.True(t, provider.SupportedRecordType(endpoint.RecordTypeMX))
	assert.False(t, provider.SupportedRecordType("SOA"))
}
