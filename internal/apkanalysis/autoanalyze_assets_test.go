package apkanalysis

import (
	"testing"

	"finduai/internal/database"
)

func TestEndpointAssetsFromReport_NilReport(t *testing.T) {
	assets := endpointAssetsFromReport("proj-1", nil)
	if len(assets) != 0 {
		t.Errorf("expected 0 assets for nil report, got %d", len(assets))
	}
}

func TestEndpointAssetsFromReport_NormalHosts(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "api.example.com", Hits: 10},
			{Host: "cdn.example.com", Hits: 5},
		},
		Endpoints: []EndpointSummary{
			{URL: "https://api.example.com/v1/users", Host: "api.example.com", Hits: 8},
			{URL: "http://legacy.example.com/old", Host: "legacy.example.com", Hits: 2},
		},
	}
	assets := endpointAssetsFromReport("proj-123", report)
	if len(assets) != 3 {
		t.Fatalf("expected 3 assets (3 unique hosts), got %d: %+v", len(assets), assets)
	}
	domains := map[string]bool{}
	for _, a := range assets {
		domains[a.Domain] = true
		if a.ProjectID != "proj-123" {
			t.Errorf("expected project proj-123, got %q", a.ProjectID)
		}
		if a.Source != "android-apk" {
			t.Errorf("expected source android-apk, got %q", a.Source)
		}
		if a.Domain == "" && a.IP == "" {
			t.Error("asset has neither domain nor IP set")
		}
	}
	for _, want := range []string{"api.example.com", "cdn.example.com", "legacy.example.com"} {
		if !domains[want] {
			t.Errorf("missing host %q in assets", want)
		}
	}
}

func TestEndpointAssetsFromReport_DuplicateHosts(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "api.example.com", Hits: 10},
			{Host: "api.example.com", Hits: 10}, // duplicate
		},
		Endpoints: []EndpointSummary{
			{URL: "https://api.example.com/v1", Host: "api.example.com", Hits: 8},
		},
	}
	assets := endpointAssetsFromReport("proj-1", report)
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset (deduplicated), got %d", len(assets))
	}
}

func TestEndpointAssetsFromReport_IPAddress(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "10.0.0.1", Hits: 3},
			{Host: "example.com", Hits: 1},
		},
	}
	assets := endpointAssetsFromReport("proj-1", report)
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(assets))
	}
	for _, a := range assets {
		if a.Domain == "10.0.0.1" {
			t.Errorf("IP 10.0.0.1 should be in IP field, not Domain")
		}
		if a.IP == "example.com" {
			t.Errorf("domain example.com should be in Domain field, not IP")
		}
	}
}

func TestEndpointAssetsFromReport_URLStyleHost(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "https://api.example.com:8443/path", Hits: 1},
			{Host: "http://192.168.1.1:3000/health", Hits: 1},
		},
	}
	assets := endpointAssetsFromReport("proj-1", report)
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d: %+v", len(assets), assets)
	}
	for _, a := range assets {
		if a.Domain != "" && len(a.Domain) > 100 {
			t.Errorf("domain too long after normalization: %q", a.Domain)
		}
		// normalizeAsset handles URL parsing; our host field should be clean
		if a.Host == "" {
			t.Error("host is empty after normalization")
		}
	}
}

func TestEndpointAssetsFromReport_EmptyResult(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{},
		Endpoints:     nil,
	}
	assets := endpointAssetsFromReport("proj-1", report)
	if len(assets) != 0 {
		t.Errorf("expected 0 assets for empty report, got %d", len(assets))
	}
}

func TestEndpointAssetsFromReport_Tagged(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "example.com", Hits: 1},
		},
	}
	assets := endpointAssetsFromReport("proj-1", report)
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if len(assets[0].Tags) != 1 || assets[0].Tags[0] != "android-apk" {
		t.Errorf("expected tags [android-apk], got %v", assets[0].Tags)
	}
	if assets[0].Status != "active" {
		t.Errorf("expected status active, got %q", assets[0].Status)
	}
}

// Verify ValidateAssetForImport passes for our generated assets.
func TestEndpointAssetsFromReport_ValidationPasses(t *testing.T) {
	report := &AutoReport{
		EndpointHosts: []HostSummary{
			{Host: "api.example.com", Hits: 10},
			{Host: "10.0.0.1", Hits: 3},
		},
	}
	assets := endpointAssetsFromReport("proj-1", report)
	for _, a := range assets {
		// ValidateAssetForImport mutates (normalizes), so make a copy
		tmp := *a
		if err := database.ValidateAssetForImport(&tmp); err != nil {
			t.Errorf("ValidateAssetForImport failed for %q: %v", a.Host, err)
		}
	}
}
