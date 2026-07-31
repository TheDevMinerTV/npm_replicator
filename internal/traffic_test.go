package internal

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type trafficResultRecorder struct {
	mu       sync.Mutex
	requests map[string]int
}

func (r *trafficResultRecorder) AddWireBytes(_, _ string, _ int64) {}

func (r *trafficResultRecorder) IncRequest(operation, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.requests == nil {
		r.requests = make(map[string]int)
	}
	r.requests[operation+"/"+result]++
}

func (r *trafficResultRecorder) requestTotal(operation, result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[operation+"/"+result]
}

type proxyRoundTripFunc func(*http.Request) (*http.Response, error)

func (f proxyRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMeterProxyTransportCountsEveryRetryAttempt(t *testing.T) {
	proxyErrors := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_proxy_errors_total"},
		[]string{"proxy"},
	)
	proxy := &roundRobinTransport{
		proxies: []*proxyState{
			{
				name: "failing",
				transport: proxyRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("proxy unavailable")
				}),
			},
			{
				name: "working",
				transport: proxyRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK}, nil
				}),
			},
		},
		errorCounter: proxyErrors,
	}
	recorder := &trafficResultRecorder{}
	transport := MeterProxyTransport(proxy, "metadata", recorder)

	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if got := recorder.requestTotal("metadata", "transport_error"); got != 1 {
		t.Errorf("transport errors = %d, want 1", got)
	}
	if got := recorder.requestTotal("metadata", "2xx"); got != 1 {
		t.Errorf("2xx requests = %d, want 1", got)
	}
}

func TestMeterTransportDoesNotMutateBaseTransport(t *testing.T) {
	base := &http.Transport{}
	recorder := &trafficResultRecorder{}

	_ = meterTransport(base, "metadata", recorder)

	if base.DialContext != nil {
		t.Error("base transport DialContext was mutated")
	}
}
