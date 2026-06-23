package thalassa

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/thalassa-cloud/client-go/dns"
	"github.com/thalassa-cloud/client-go/thalassa"
	"golang.org/x/sync/errgroup"

	"github.com/thalassa-cloud/external-dns-thalassa-webhook/internal/metrics"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

const defaultTTL = 300

var _ provider.Provider = (*Provider)(nil)

type dnsAPI interface {
	ListZones(ctx context.Context, req *dns.ListZonesRequest) ([]dns.DnsZone, error)
	ListRecords(ctx context.Context, zoneIdentity string, req *dns.ListRecordsRequest) ([]dns.DnsRecord, error)
	CreateRecord(ctx context.Context, zoneIdentity string, create dns.CreateDnsRecordRequest) (*dns.DnsRecord, error)
	UpdateRecord(ctx context.Context, zoneIdentity, recordIdentity string, update dns.UpdateDnsRecordRequest) (*dns.DnsRecord, error)
	DeleteRecord(ctx context.Context, zoneIdentity, recordIdentity string) error
}

type Provider struct {
	provider.BaseProvider
	client       dnsAPI
	zoneIdentity map[string]string
	domainFilter *endpoint.DomainFilter
	dryRun       bool
	workers      int

	log *slog.Logger
}

type changeCreate struct {
	ZoneName string
	ZoneID   string
	Request  dns.CreateDnsRecordRequest
}

type changeUpdate struct {
	ZoneName string
	ZoneID   string
	Record   dns.DnsRecord
	Request  dns.UpdateDnsRecordRequest
}

type changeDelete struct {
	ZoneName string
	ZoneID   string
	Record   dns.DnsRecord
}

type changes struct {
	Creates []*changeCreate
	Updates []*changeUpdate
	Deletes []*changeDelete
}

func (c *changes) Empty() bool {
	return len(c.Creates) == 0 && len(c.Updates) == 0 && len(c.Deletes) == 0
}

func NewProvider(cfg *Config) (*Provider, error) {
	tc, err := newThalassaClient(cfg)
	if err != nil {
		return nil, err
	}

	return &Provider{
		client:       tc.DNS(),
		domainFilter: endpoint.NewDomainFilter(cfg.DomainFilter),
		dryRun:       cfg.DryRun,
		workers:      cfg.Workers,
		log:          slog.With("component", "provider"),
	}, nil
}

func newThalassaClient(cfg *Config) (thalassa.Client, error) {
	opts, err := buildClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	tc, err := thalassa.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Thalassa client: %w", err)
	}

	slog.Info("Thalassa provider configured with retry", "max", cfg.HTTPRetryMax, "waitMin", cfg.HTTPRetryWaitMin, "waitMax", cfg.HTTPRetryWaitMax)

	return tc, nil
}

func (p *Provider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	zones, err := p.zones(ctx)
	if err != nil {
		return nil, err
	}

	recordsByZone, err := p.fetchRecordsParallel(ctx, zones)
	if err != nil {
		return nil, err
	}

	var endpoints []*endpoint.Endpoint
	for _, zone := range zones {
		for _, record := range recordsByZone[zone.Name] {
			if !p.SupportedRecordType(string(record.Type)) {
				continue
			}

			fqdn := dns.RecordFQDN(zone.Name, record.Name)
			for _, value := range record.Values {
				target := formatRecordTarget(string(record.Type), value)
				ep := endpoint.NewEndpointWithTTL(fqdn, string(record.Type), endpoint.TTL(record.TTL), target)
				endpoints = append(endpoints, ep)
			}
		}
	}

	endpoints = mergeEndpointsByNameType(endpoints)

	p.log.Debug("Endpoints generated from Thalassa DNS", "endpoints", len(endpoints))

	return endpoints, nil
}

func (p *Provider) ApplyChanges(ctx context.Context, planChanges *plan.Changes) error {
	recordsByZone, zoneNameIDMapper, err := p.getRecordsByZone(ctx)
	if err != nil {
		return err
	}

	createsByZone := endpointsByZone(zoneNameIDMapper, planChanges.Create)
	updatesByZone := endpointsByZone(zoneNameIDMapper, planChanges.UpdateNew)
	deletesByZone := endpointsByZone(zoneNameIDMapper, planChanges.Delete)

	var chg changes

	if err := processCreateActions(recordsByZone, createsByZone, &chg); err != nil {
		return err
	}
	if err := processUpdateActions(recordsByZone, updatesByZone, &chg); err != nil {
		return err
	}
	if err := processDeleteActions(recordsByZone, deletesByZone, &chg); err != nil {
		return err
	}

	p.populateZoneIDs(&chg)

	return p.submitChanges(ctx, &chg)
}

func (p *Provider) SupportedRecordType(recordType string) bool {
	switch recordType {
	case endpoint.RecordTypeMX:
		return true
	default:
		return provider.SupportedRecordType(recordType)
	}
}

func (p *Provider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

func (p *Provider) zones(ctx context.Context) ([]dns.DnsZone, error) {
	allZones, err := p.client.ListZones(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list zones: %w", err)
	}

	var result []dns.DnsZone
	identities := make(map[string]string, len(allZones))

	for _, zone := range allZones {
		if p.domainFilter.Match(zone.Name) {
			result = append(result, zone)
			identities[zone.Name] = zone.Identity
		}
	}

	p.zoneIdentity = identities
	return result, nil
}

func (p *Provider) getRecordsByZone(ctx context.Context) (map[string][]dns.DnsRecord, provider.ZoneIDName, error) {
	zones, err := p.zones(ctx)
	if err != nil {
		return nil, nil, err
	}

	zoneNameIDMapper := provider.ZoneIDName{}
	for _, zone := range zones {
		zoneNameIDMapper.Add(zone.Name, zone.Name)
	}

	recordsByZone, err := p.fetchRecordsParallel(ctx, zones)
	if err != nil {
		return nil, nil, err
	}

	return recordsByZone, zoneNameIDMapper, nil
}

func (p *Provider) fetchRecordsParallel(ctx context.Context, zones []dns.DnsZone) (map[string][]dns.DnsRecord, error) {
	recordsByZone := make(map[string][]dns.DnsRecord)
	if len(zones) == 0 {
		return recordsByZone, nil
	}

	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)

	workers := p.workers
	if workers < 1 {
		workers = 1
	}

	maxConcurrentZones := min(len(zones), workers)
	g.SetLimit(maxConcurrentZones)

	for _, zone := range zones {
		g.Go(func() error {
			records, err := p.client.ListRecords(ctx, zone.Identity, nil)
			if err != nil {
				return fmt.Errorf("failed to list records for zone %s: %w", zone.Name, err)
			}

			mu.Lock()
			recordsByZone[zone.Name] = records
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return recordsByZone, nil
}

func (p *Provider) submitChanges(ctx context.Context, chg *changes) error {
	if chg.Empty() {
		return nil
	}

	var errs []error

	for _, c := range chg.Creates {
		logger := p.log.With("zone", c.ZoneName, "dnsName", c.Request.Name,
			"recordType", c.Request.Type,
			"values", c.Request.Values,
			"ttl", c.Request.TTL,
		)
		logger.Info("Creating DNS record")

		if p.dryRun {
			continue
		}

		metrics.ThalassaRequestsTotal.Inc()
		_, err := p.client.CreateRecord(ctx, c.ZoneID, c.Request)
		if err != nil {
			metrics.ThalassaErrorsTotal.Inc()
			logger.Error("Failed to create record", "error", err)
			errs = append(errs, fmt.Errorf("create %s.%s: %w", c.Request.Name, c.ZoneName, err))
		}
	}

	for _, u := range chg.Updates {
		logger := p.log.With(
			"zone", u.ZoneName,
			"dnsName", u.Record.Name,
			"recordType", u.Record.Type,
			"values", u.Request.Values,
			"ttl", u.Request.TTL,
			"recordID", u.Record.Identity,
		)
		logger.Info("Updating DNS record")

		if p.dryRun {
			continue
		}

		metrics.ThalassaRequestsTotal.Inc()
		_, err := p.client.UpdateRecord(ctx, u.ZoneID, u.Record.Identity, u.Request)
		if err != nil {
			metrics.ThalassaErrorsTotal.Inc()
			logger.Error("Failed to update record", "error", err)
			errs = append(errs, fmt.Errorf("update %s.%s: %w", u.Record.Name, u.ZoneName, err))
		}
	}

	for _, d := range chg.Deletes {
		logger := p.log.With(
			"zone", d.ZoneName,
			"recordID", d.Record.Identity,
			"dnsName", d.Record.Name,
		)
		logger.Info("Deleting DNS record")

		if p.dryRun {
			continue
		}

		metrics.ThalassaRequestsTotal.Inc()
		err := p.client.DeleteRecord(ctx, d.ZoneID, d.Record.Identity)
		if err != nil {
			metrics.ThalassaErrorsTotal.Inc()
			logger.Error("Failed to delete record", "error", err)
			errs = append(errs, fmt.Errorf("delete record %s in %s: %w", d.Record.Identity, d.ZoneName, err))
		}
	}

	if len(errs) > 0 {
		return provider.NewSoftError(fmt.Errorf("some changes failed (%d errors): %v", len(errs), errs))
	}

	return nil
}

func mergeEndpointsByNameType(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint {
	endpointsByNameType := map[string][]*endpoint.Endpoint{}

	for _, e := range endpoints {
		key := fmt.Sprintf("%s-%s", e.DNSName, e.RecordType)
		endpointsByNameType[key] = append(endpointsByNameType[key], e)
	}

	if len(endpointsByNameType) == len(endpoints) {
		return endpoints
	}

	var result []*endpoint.Endpoint
	for _, eps := range endpointsByNameType {
		targets := make([]string, len(eps))
		for i, e := range eps {
			targets[i] = e.Targets[0]
		}
		result = append(result, endpoint.NewEndpoint(eps[0].DNSName, eps[0].RecordType, targets...))
	}

	return result
}

func endpointsByZone(zoneNameIDMapper provider.ZoneIDName, endpoints []*endpoint.Endpoint) map[string][]*endpoint.Endpoint {
	result := make(map[string][]*endpoint.Endpoint)

	for _, ep := range endpoints {
		zoneID, _ := zoneNameIDMapper.FindZone(ep.DNSName)
		if zoneID == "" {
			slog.Debug("Skipping record because no hosted zone matching record DNS Name was detected", "dnsName", ep.DNSName)
			continue
		}
		result[zoneID] = append(result[zoneID], ep)
	}

	return result
}

func getMatchingRecords(records []dns.DnsRecord, zoneName string, ep *endpoint.Endpoint) []dns.DnsRecord {
	name := recordNameFromEndpoint(zoneName, ep.DNSName)

	var result []dns.DnsRecord
	for _, record := range records {
		if record.Name == name && string(record.Type) == ep.RecordType {
			result = append(result, record)
		}
	}
	return result
}

func recordNameFromEndpoint(zoneName, dnsName string) string {
	if dnsName == zoneName {
		return "@"
	}
	return strings.TrimSuffix(dnsName, "."+zoneName)
}

func getTTLFromEndpoint(ep *endpoint.Endpoint) int {
	if ep.RecordTTL.IsConfigured() {
		return int(ep.RecordTTL)
	}
	return defaultTTL
}

func formatRecordTarget(recordType, value string) string {
	if recordType == endpoint.RecordTypeCNAME || recordType == endpoint.RecordTypeMX {
		return strings.TrimSuffix(value, ".")
	}
	return value
}

func makeCreateRequest(zoneName, dnsName, recordType, target string, ttl int) dns.CreateDnsRecordRequest {
	return dns.CreateDnsRecordRequest{
		Name:   recordNameFromEndpoint(zoneName, dnsName),
		Type:   dns.DnsRecordType(recordType),
		TTL:    ttl,
		Values: []string{formatRecordValue(recordType, target)},
	}
}

func makeUpdateRequest(recordType, target string, ttl int) dns.UpdateDnsRecordRequest {
	return dns.UpdateDnsRecordRequest{
		TTL:    ttl,
		Values: []string{formatRecordValue(recordType, target)},
	}
}

func formatRecordValue(recordType, target string) string {
	switch recordType {
	case endpoint.RecordTypeCNAME, endpoint.RecordTypeMX:
		if mxRecord, err := endpoint.NewMXRecord(target); err == nil {
			return dns.FormatMX(int(*mxRecord.GetPriority()), strings.TrimSuffix(*mxRecord.GetHost(), "."))
		}
		return strings.TrimSuffix(target, ".")
	default:
		return target
	}
}

func recordTargetMatches(record dns.DnsRecord, recordType, target string) bool {
	for _, value := range record.Values {
		if targetMatchesValue(recordType, target, value) {
			return true
		}
	}
	return false
}

func targetMatchesValue(recordType, target, value string) bool {
	switch recordType {
	case endpoint.RecordTypeMX:
		targetHost := target
		valueHost := value
		if mxRecord, err := endpoint.NewMXRecord(target); err == nil {
			targetHost = provider.EnsureTrailingDot(*mxRecord.GetHost())
		}
		parts := strings.Fields(value)
		if len(parts) == 2 {
			valueHost = provider.EnsureTrailingDot(parts[1])
		}
		return strings.TrimSuffix(targetHost, ".") == strings.TrimSuffix(valueHost, ".")
	case endpoint.RecordTypeCNAME:
		return strings.TrimSuffix(target, ".") == strings.TrimSuffix(value, ".")
	default:
		return target == value
	}
}

func processCreateActions(recordsByZone map[string][]dns.DnsRecord, createsByZone map[string][]*endpoint.Endpoint, chg *changes) error {
	for zoneName, endpoints := range createsByZone {
		if len(endpoints) == 0 {
			continue
		}

		records := recordsByZone[zoneName]

		for _, ep := range endpoints {
			matchingRecords := getMatchingRecords(records, zoneName, ep)
			if len(matchingRecords) > 0 {
				slog.Warn("Preexisting records exist which should not exist for creation actions",
					"zone", zoneName,
					"dnsName", ep.DNSName,
					"recordType", ep.RecordType,
				)
			}

			ttl := getTTLFromEndpoint(ep)
			for _, target := range ep.Targets {
				chg.Creates = append(chg.Creates, &changeCreate{
					ZoneName: zoneName,
					Request:  makeCreateRequest(zoneName, ep.DNSName, ep.RecordType, target, ttl),
				})
			}
		}
	}

	return nil
}

func processUpdateActions(recordsByZone map[string][]dns.DnsRecord, updatesByZone map[string][]*endpoint.Endpoint, chg *changes) error {
	for zoneName, updates := range updatesByZone {
		if len(updates) == 0 {
			continue
		}

		records := recordsByZone[zoneName]

		for _, ep := range updates {
			matchingRecords := getMatchingRecords(records, zoneName, ep)

			if len(matchingRecords) == 0 {
				slog.Warn("Planning an update but no existing records found",
					"zone", zoneName,
					"dnsName", ep.DNSName,
					"recordType", ep.RecordType,
				)
			}

			matchingRecordsByTarget := map[string]dns.DnsRecord{}
			for _, record := range matchingRecords {
				for _, value := range record.Values {
					key := value
					if ep.RecordType == endpoint.RecordTypeCNAME || ep.RecordType == endpoint.RecordTypeMX {
						key = strings.TrimSuffix(value, ".")
					}
					matchingRecordsByTarget[key] = record
				}
			}

			ttl := getTTLFromEndpoint(ep)

			for _, target := range ep.Targets {
				lookupKey := target
				if ep.RecordType == endpoint.RecordTypeCNAME || ep.RecordType == endpoint.RecordTypeMX {
					lookupKey = strings.TrimSuffix(target, ".")
				}

				if record, ok := matchingRecordsByTarget[lookupKey]; ok {
					chg.Updates = append(chg.Updates, &changeUpdate{
						ZoneName: zoneName,
						Record:   record,
						Request:  makeUpdateRequest(ep.RecordType, target, ttl),
					})
					delete(matchingRecordsByTarget, lookupKey)
				} else {
					chg.Creates = append(chg.Creates, &changeCreate{
						ZoneName: zoneName,
						Request:  makeCreateRequest(zoneName, ep.DNSName, ep.RecordType, target, ttl),
					})
				}
			}

			for _, record := range matchingRecordsByTarget {
				chg.Deletes = append(chg.Deletes, &changeDelete{
					ZoneName: zoneName,
					Record:   record,
				})
			}
		}
	}

	return nil
}

func processDeleteActions(recordsByZone map[string][]dns.DnsRecord, deletesByZone map[string][]*endpoint.Endpoint, chg *changes) error {
	for zoneName, deletes := range deletesByZone {
		if len(deletes) == 0 {
			continue
		}

		records := recordsByZone[zoneName]

		for _, ep := range deletes {
			matchingRecords := getMatchingRecords(records, zoneName, ep)

			if len(matchingRecords) == 0 {
				slog.Warn("Records to delete not found",
					"zone", zoneName,
					"dnsName", ep.DNSName,
					"recordType", ep.RecordType,
				)
			}

			for _, record := range matchingRecords {
				doDelete := len(ep.Targets) == 0
				for _, target := range ep.Targets {
					if recordTargetMatches(record, ep.RecordType, target) {
						doDelete = true
						break
					}
				}

				if doDelete {
					chg.Deletes = append(chg.Deletes, &changeDelete{
						ZoneName: zoneName,
						Record:   record,
					})
				}
			}
		}
	}

	return nil
}

func (p *Provider) zoneID(zoneName string) string {
	if p.zoneIdentity != nil {
		if id, ok := p.zoneIdentity[zoneName]; ok {
			return id
		}
	}
	return ""
}

func (p *Provider) populateZoneIDs(chg *changes) {
	for _, c := range chg.Creates {
		c.ZoneID = p.zoneID(c.ZoneName)
	}
	for _, u := range chg.Updates {
		u.ZoneID = p.zoneID(u.ZoneName)
	}
	for _, d := range chg.Deletes {
		d.ZoneID = p.zoneID(d.ZoneName)
	}
}
