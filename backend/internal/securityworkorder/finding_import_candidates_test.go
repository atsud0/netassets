package securityworkorder

import (
	"testing"

	"netassets/internal/asset"
)

func TestExactFindingImportScopesUsesLedgerNormalization(t *testing.T) {
	scopes := []findingImportScope{
		{ScopeAssetID: "scope-domain", AssetID: "asset-domain", AssetTypeSnapshot: "domain", AssetValueSnapshot: "Example.COM."},
		{ScopeAssetID: "scope-url", AssetID: "asset-url", AssetTypeSnapshot: "url", AssetValueSnapshot: "https://Example.com/login#old"},
	}
	domainValue, err := asset.NormalizeValue("domain", "example.com")
	if err != nil {
		t.Fatalf("normalize domain: %v", err)
	}
	matches := exactFindingImportScopes(scopes, "domain", "example.com", domainValue)
	if len(matches) != 1 || matches[0].ScopeAssetID != "scope-domain" {
		t.Fatalf("domain matches = %#v, want scope-domain", matches)
	}
	urlValue, err := asset.NormalizeValue("url", "https://example.com/login#scanner")
	if err != nil {
		t.Fatalf("normalize url: %v", err)
	}
	matches = exactFindingImportScopes(scopes, "url", "https://example.com/login#scanner", urlValue)
	if len(matches) != 1 || matches[0].ScopeAssetID != "scope-url" {
		t.Fatalf("url matches = %#v, want scope-url", matches)
	}
}

func TestExactFindingImportScopesDoesNotGuessAmbiguousAssets(t *testing.T) {
	scopes := []findingImportScope{
		{ScopeAssetID: "scope-a", AssetID: "asset-a", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.10"},
		{ScopeAssetID: "scope-b", AssetID: "asset-b", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.10"},
	}
	normalized, err := asset.NormalizeValue("ip", "192.0.2.10")
	if err != nil {
		t.Fatalf("normalize ip: %v", err)
	}
	matches := exactFindingImportScopes(scopes, "ip", "192.0.2.10", normalized)
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 so importer can send the row to manual review", len(matches))
	}
}

func TestSuggestFindingImportScopeKeepsLooseMatchesForReview(t *testing.T) {
	tests := []struct {
		name       string
		scope      findingImportScope
		assetType  string
		assetValue string
		method     string
		confidence int
	}{
		{name: "ip with port", scope: findingImportScope{ScopeAssetID: "ip", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.10"}, assetType: "ip", assetValue: "192.0.2.10:443", method: "endpoint_host", confidence: 90},
		{name: "cidr contains ip", scope: findingImportScope{ScopeAssetID: "cidr", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.10"}, assetType: "ip", assetValue: "192.0.2.0/24", method: "cidr_contains", confidence: 70},
		{name: "url to domain", scope: findingImportScope{ScopeAssetID: "domain", AssetTypeSnapshot: "domain", AssetValueSnapshot: "example.com"}, assetType: "url", assetValue: "https://example.com/login", method: "cross_type_host", confidence: 75},
		{name: "wildcard domain", scope: findingImportScope{ScopeAssetID: "wildcard", AssetTypeSnapshot: "domain", AssetValueSnapshot: "portal.example.com"}, assetType: "domain", assetValue: "*.example.com", method: "wildcard_domain", confidence: 65},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestion, method, confidence := suggestFindingImportScope([]findingImportScope{tt.scope}, tt.assetType, tt.assetValue, "")
			if suggestion.ScopeAssetID != tt.scope.ScopeAssetID || method != tt.method || confidence != tt.confidence {
				t.Fatalf("suggestion=(%s,%s,%d), want (%s,%s,%d)", suggestion.ScopeAssetID, method, confidence, tt.scope.ScopeAssetID, tt.method, tt.confidence)
			}
		})
	}
}

func TestSuggestFindingImportScopeDoesNotSelectTiedCIDRMatches(t *testing.T) {
	scopes := []findingImportScope{
		{ScopeAssetID: "scope-a", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.10"},
		{ScopeAssetID: "scope-b", AssetTypeSnapshot: "ip", AssetValueSnapshot: "192.0.2.11"},
	}
	suggestion, method, confidence := suggestFindingImportScope(scopes, "ip", "192.0.2.0/24", "")
	if suggestion.ScopeAssetID != "" || method != "cidr_contains" || confidence != 70 {
		t.Fatalf("suggestion=(%s,%s,%d), want no automatic suggestion with cidr metadata", suggestion.ScopeAssetID, method, confidence)
	}
}

func TestFindingImportCandidateFindingStatusUsesExportVocabulary(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   string
	}{
		{status: "pending", want: "pending"},
		{status: "in_progress", want: "in_progress"},
		{status: "忽略", want: "ignored"},
		{status: "", want: "pending"},
	} {
		candidate := FindingImportCandidate{RawData: map[string]string{"status": tc.status}}
		if got := findingImportCandidateFindingStatus(candidate); got != tc.want {
			t.Fatalf("status %q -> %q, want %q", tc.status, got, tc.want)
		}
	}
}
