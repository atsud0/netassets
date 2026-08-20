package assetobservation

import "time"

const (
	TypeDomainRegistration = "domain_registration"
	TypeTLSCertificate     = "tls_certificate"

	StatusPending     = "pending"
	StatusSuccess     = "success"
	StatusFailed      = "failed"
	StatusUnsupported = "unsupported"
)

type Observation struct {
	ID                  string         `json:"id"`
	AssetID             string         `json:"asset_id"`
	ObservationType     string         `json:"observation_type"`
	EndpointKey         string         `json:"endpoint_key"`
	Status              string         `json:"status"`
	Source              string         `json:"source,omitempty"`
	SourceReference     string         `json:"source_reference,omitempty"`
	NotBefore           *time.Time     `json:"not_before,omitempty"`
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
	Metadata            map[string]any `json:"metadata"`
	LastAttemptAt       time.Time      `json:"last_attempt_at"`
	LastSuccessAt       *time.Time     `json:"last_success_at,omitempty"`
	NextRefreshAt       time.Time      `json:"next_refresh_at"`
	ConsecutiveFailures int            `json:"consecutive_failures"`
	ErrorCode           string         `json:"error_code,omitempty"`
	ErrorMessage        string         `json:"error_message,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type RefreshResult struct {
	Items       []Observation `json:"items"`
	Successes   int           `json:"successes"`
	Failures    int           `json:"failures"`
	Unsupported int           `json:"unsupported"`
}

type collectionResult struct {
	ObservationType string
	EndpointKey     string
	Status          string
	Source          string
	SourceReference string
	NotBefore       *time.Time
	ExpiresAt       *time.Time
	Metadata        map[string]any
	ErrorCode       string
	ErrorMessage    string
	RefreshAfter    time.Duration
}

type assetTarget struct {
	ID              string
	AssetType       string
	Value           string
	NormalizedValue string
	NetworkScope    string
	OrganizationID  string
}
