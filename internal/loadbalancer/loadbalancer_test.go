package loadbalancer

import (
	"net/url"
	"sync"
	"testing"
)

func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}

func createTestBackends() []*Backend {
	return []*Backend{
		{URL: mustParseURL("http://backend1:8080"), Weight: 1, IsHealthy: true},
		{URL: mustParseURL("http://backend2:8080"), Weight: 1, IsHealthy: true},
		{URL: mustParseURL("http://backend3:8080"), Weight: 1, IsHealthy: true},
	}
}

func TestRoundRobinDistribution(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	counts := make(map[string]int)
	iterations := 300

	for i := 0; i < iterations; i++ {
		backend := lb.Select()
		if backend == nil {
			t.Fatal("expected backend, got nil")
		}
		counts[backend.URL.String()]++
	}

	// Each backend should get roughly equal requests
	expectedPerBackend := iterations / len(backends)
	tolerance := 10 // allow some variance

	for url, count := range counts {
		if count < expectedPerBackend-tolerance || count > expectedPerBackend+tolerance {
			t.Errorf("backend %s got %d requests, expected ~%d", url, count, expectedPerBackend)
		}
	}
}

func TestRoundRobinSkipsUnhealthy(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	// Mark backend2 as unhealthy
	lb.SetHealthy("http://backend2:8080", false)

	counts := make(map[string]int)
	iterations := 100

	for i := 0; i < iterations; i++ {
		backend := lb.Select()
		if backend == nil {
			t.Fatal("expected backend, got nil")
		}
		counts[backend.URL.String()]++
	}

	// backend2 should not receive any requests
	if counts["http://backend2:8080"] > 0 {
		t.Errorf("unhealthy backend2 received %d requests, expected 0", counts["http://backend2:8080"])
	}

	// backend1 and backend3 should share the load
	if counts["http://backend1:8080"] == 0 {
		t.Error("healthy backend1 received no requests")
	}
	if counts["http://backend3:8080"] == 0 {
		t.Error("healthy backend3 received no requests")
	}
}

func TestAllUnhealthyReturnsNil(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	// Mark all backends as unhealthy
	lb.SetHealthy("http://backend1:8080", false)
	lb.SetHealthy("http://backend2:8080", false)
	lb.SetHealthy("http://backend3:8080", false)

	backend := lb.Select()
	if backend != nil {
		t.Errorf("expected nil when all backends unhealthy, got %v", backend.URL)
	}
}

func TestSetHealthy(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	// Initially healthy
	if lb.HealthyCount() != 3 {
		t.Errorf("expected 3 healthy backends, got %d", lb.HealthyCount())
	}

	// Mark one as unhealthy
	lb.SetHealthy("http://backend1:8080", false)
	if lb.HealthyCount() != 2 {
		t.Errorf("expected 2 healthy backends, got %d", lb.HealthyCount())
	}

	// Mark it back as healthy
	lb.SetHealthy("http://backend1:8080", true)
	if lb.HealthyCount() != 3 {
		t.Errorf("expected 3 healthy backends, got %d", lb.HealthyCount())
	}
}

func TestConcurrency(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	var wg sync.WaitGroup
	goroutines := 100
	iterations := 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				backend := lb.Select()
				if backend == nil {
					t.Error("expected backend, got nil")
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestConcurrencyWithHealthUpdates(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	var wg sync.WaitGroup

	// Goroutine 1: continuously select backends
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			lb.Select()
		}
	}()

	// Goroutine 2: continuously update health
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			lb.SetHealthy("http://backend1:8080", false)
			lb.SetHealthy("http://backend1:8080", true)
		}
	}()

	wg.Wait()
}

func TestEmptyBackends(t *testing.T) {
	lb := New("round-robin", []*Backend{})

	backend := lb.Select()
	if backend != nil {
		t.Errorf("expected nil for empty backends, got %v", backend)
	}
}

func TestRandomSelector(t *testing.T) {
	backends := createTestBackends()
	lb := New("random", backends)

	counts := make(map[string]int)
	iterations := 300

	for i := 0; i < iterations; i++ {
		backend := lb.Select()
		if backend == nil {
			t.Fatal("expected backend, got nil")
		}
		counts[backend.URL.String()]++
	}

	// All backends should receive at least some requests
	for url := range counts {
		if counts[url] == 0 {
			t.Errorf("backend %s received 0 requests", url)
		}
	}
}

func TestRandomSelectorSkipsUnhealthy(t *testing.T) {
	backends := createTestBackends()
	lb := New("random", backends)

	lb.SetHealthy("http://backend2:8080", false)

	for i := 0; i < 100; i++ {
		backend := lb.Select()
		if backend == nil {
			t.Fatal("expected backend, got nil")
		}
		if backend.URL.String() == "http://backend2:8080" {
			t.Error("random selector selected unhealthy backend")
		}
	}
}

func TestGetBackends(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	result := lb.GetBackends()
	if len(result) != 3 {
		t.Errorf("expected 3 backends, got %d", len(result))
	}
}

func TestHealthyCount(t *testing.T) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	if lb.HealthyCount() != 3 {
		t.Errorf("expected 3 healthy, got %d", lb.HealthyCount())
	}

	lb.SetHealthy("http://backend1:8080", false)
	lb.SetHealthy("http://backend2:8080", false)

	if lb.HealthyCount() != 1 {
		t.Errorf("expected 1 healthy, got %d", lb.HealthyCount())
	}
}

func BenchmarkRoundRobinSelect(b *testing.B) {
	backends := createTestBackends()
	lb := New("round-robin", backends)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lb.Select()
	}
}

func BenchmarkRandomSelect(b *testing.B) {
	backends := createTestBackends()
	lb := New("random", backends)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lb.Select()
	}
}
