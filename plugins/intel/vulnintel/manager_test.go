package vulnintel

import (
	"testing"

	dfe "finduai/domain/finding"
)

type testStore struct{}

func (testStore) UpsertVulnIntel(dfe.VulnIntel) (bool, error)   { return false, nil }
func (testStore) PendingVulnIntel(int) ([]dfe.VulnIntel, error) { return nil, nil }
func (testStore) EnrichVulnIntel(dfe.VulnIntelEnrich) error     { return nil }

type testSettings struct{}

func (testSettings) GetSetting(string) (string, bool, error) { return "", false, nil }

func TestManagerStartStopIsIdempotent(t *testing.T) {
	m := NewManager(testStore{}, testSettings{})
	m.Start()
	m.Start()
	if _, _, _, sources := m.Status(); sources != 9 {
		t.Fatalf("sources=%d", sources)
	}
	m.Stop()
	m.Stop()

	// Stop 后可以再次启动，生命周期不依赖 server.Manager。
	m.Start()
	m.Stop()
}
