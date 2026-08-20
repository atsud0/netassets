package assetobservation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const ianaDNSBootstrapURL = "https://data.iana.org/rdap/dns.json"

var (
	errRDAPUnsupported = errors.New("rdap service is unavailable for the domain suffix")
	errRDAPNoExpiry    = errors.New("rdap response does not contain an expiration event")
)

type domainLookupResult struct {
	RegisteredDomain string
	ExpiresAt        time.Time
	SourceReference  string
	Metadata         map[string]any
}

type domainLookup interface {
	Lookup(context.Context, string) (domainLookupResult, error)
}

type rdapClient struct {
	httpClient   *http.Client
	bootstrapURL string
	now          func() time.Time

	mu               sync.Mutex
	servicesBySuffix map[string][]string
	bootstrapUntil   time.Time
}

type rdapBootstrap struct {
	Services [][][]string `json:"services"`
}

type rdapDomainResponse struct {
	Handle      string   `json:"handle"`
	LDHName     string   `json:"ldhName"`
	UnicodeName string   `json:"unicodeName"`
	Status      []string `json:"status"`
	Events      []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
		Actor  string `json:"eventActor"`
	} `json:"events"`
	Entities []struct {
		Roles     []string `json:"roles"`
		PublicIDs []struct {
			Type       string `json:"type"`
			Identifier string `json:"identifier"`
		} `json:"publicIds"`
		VCardArray json.RawMessage `json:"vcardArray"`
	} `json:"entities"`
}

func newRDAPClient() *rdapClient {
	dialer := newPublicDialer(8 * time.Second)
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || !strings.EqualFold(req.URL.Scheme, "https") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return &rdapClient{httpClient: client, bootstrapURL: ianaDNSBootstrapURL, now: time.Now}
}

func registrableDomain(raw string) (string, error) {
	host := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(raw), "."))
	host = strings.TrimPrefix(host, "*.")
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" || !strings.Contains(ascii, ".") {
		return "", errRDAPUnsupported
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return "", errRDAPUnsupported
	}
	return strings.ToLower(root), nil
}

func (c *rdapClient) Lookup(ctx context.Context, rawDomain string) (domainLookupResult, error) {
	domain, err := registrableDomain(rawDomain)
	if err != nil {
		return domainLookupResult{}, err
	}
	services, err := c.services(ctx)
	if err != nil {
		return domainLookupResult{}, err
	}
	parts := strings.Split(domain, ".")
	suffix := parts[len(parts)-1]
	bases := services[suffix]
	if len(bases) == 0 {
		return domainLookupResult{}, errRDAPUnsupported
	}

	var lastErr error
	for _, base := range bases {
		result, lookupErr := c.lookupAt(ctx, base, domain)
		if lookupErr == nil {
			return result, nil
		}
		lastErr = lookupErr
	}
	if lastErr == nil {
		lastErr = errRDAPUnsupported
	}
	return domainLookupResult{}, lastErr
}

func (c *rdapClient) services(ctx context.Context) (map[string][]string, error) {
	c.mu.Lock()
	if len(c.servicesBySuffix) > 0 && c.now().Before(c.bootstrapUntil) {
		cached := c.servicesBySuffix
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.bootstrapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rdap bootstrap: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch rdap bootstrap: http %d", resp.StatusCode)
	}
	var payload rdapBootstrap
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode rdap bootstrap: %w", err)
	}
	services := parseBootstrapServices(payload)
	if len(services) == 0 {
		return nil, errors.New("rdap bootstrap contains no services")
	}
	c.mu.Lock()
	c.servicesBySuffix = services
	c.bootstrapUntil = c.now().Add(24 * time.Hour)
	c.mu.Unlock()
	return services, nil
}

func parseBootstrapServices(payload rdapBootstrap) map[string][]string {
	result := map[string][]string{}
	for _, service := range payload.Services {
		if len(service) != 2 {
			continue
		}
		for _, suffix := range service[0] {
			suffix = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
			if suffix == "" {
				continue
			}
			for _, base := range service[1] {
				parsed, err := url.Parse(strings.TrimSpace(base))
				if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
					continue
				}
				normalized := strings.TrimRight(parsed.String(), "/") + "/"
				if !containsString(result[suffix], normalized) {
					result[suffix] = append(result[suffix], normalized)
				}
			}
		}
	}
	for suffix := range result {
		sort.Strings(result[suffix])
	}
	return result
}

func (c *rdapClient) lookupAt(ctx context.Context, base, domain string) (domainLookupResult, error) {
	endpoint := strings.TrimRight(base, "/") + "/domain/" + url.PathEscape(domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domainLookupResult{}, err
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domainLookupResult{}, fmt.Errorf("query rdap domain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return domainLookupResult{}, errRDAPUnsupported
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domainLookupResult{}, fmt.Errorf("query rdap domain: http %d", resp.StatusCode)
	}
	var payload rdapDomainResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return domainLookupResult{}, fmt.Errorf("decode rdap domain: %w", err)
	}
	expiresAt, events, err := rdapExpiration(payload.Events)
	if err != nil {
		return domainLookupResult{}, err
	}
	registrar := rdapRegistrar(payload)
	metadata := map[string]any{
		"registered_domain": domain,
		"rdap_handle":       payload.Handle,
		"ldh_name":          payload.LDHName,
		"unicode_name":      payload.UnicodeName,
		"statuses":          payload.Status,
		"events":            events,
	}
	if registrar != "" {
		metadata["registrar"] = registrar
	}
	return domainLookupResult{RegisteredDomain: domain, ExpiresAt: expiresAt, SourceReference: endpoint, Metadata: metadata}, nil
}

func rdapExpiration(events []struct {
	Action string `json:"eventAction"`
	Date   string `json:"eventDate"`
	Actor  string `json:"eventActor"`
}) (time.Time, []map[string]string, error) {
	var registrarExpiration time.Time
	var registryExpiration time.Time
	items := make([]map[string]string, 0, len(events))
	for _, event := range events {
		action := strings.ToLower(strings.TrimSpace(event.Action))
		date := strings.TrimSpace(event.Date)
		if action == "" || date == "" {
			continue
		}
		item := map[string]string{"action": action, "date": date}
		if strings.TrimSpace(event.Actor) != "" {
			item["actor"] = strings.TrimSpace(event.Actor)
		}
		items = append(items, item)
		parsed, err := time.Parse(time.RFC3339, date)
		if err != nil {
			continue
		}
		switch action {
		case "registrar expiration":
			if registrarExpiration.IsZero() || parsed.After(registrarExpiration) {
				registrarExpiration = parsed
			}
		case "expiration":
			if registryExpiration.IsZero() || parsed.After(registryExpiration) {
				registryExpiration = parsed
			}
		}
	}
	if !registrarExpiration.IsZero() {
		return registrarExpiration, items, nil
	}
	if !registryExpiration.IsZero() {
		return registryExpiration, items, nil
	}
	return time.Time{}, items, errRDAPNoExpiry
}

func rdapRegistrar(payload rdapDomainResponse) string {
	for _, entity := range payload.Entities {
		if !containsStringFold(entity.Roles, "registrar") {
			continue
		}
		for _, publicID := range entity.PublicIDs {
			if strings.EqualFold(publicID.Type, "IANA Registrar ID") && strings.TrimSpace(publicID.Identifier) != "" {
				return "IANA Registrar ID " + strings.TrimSpace(publicID.Identifier)
			}
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsStringFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
