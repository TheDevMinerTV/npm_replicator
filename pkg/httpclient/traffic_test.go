package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type trafficRecord struct {
	operation string
	value     string
	bytes     int64
}

type recordingTrafficRecorder struct {
	mu       sync.Mutex
	bytes    []trafficRecord
	requests []trafficRecord
}

func (r *recordingTrafficRecorder) AddWireBytes(operation, direction string, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bytes = append(r.bytes, trafficRecord{operation: operation, value: direction, bytes: bytes})
}

func (r *recordingTrafficRecorder) IncRequest(operation, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, trafficRecord{operation: operation, value: result})
}

func (r *recordingTrafficRecorder) byteTotal(operation, direction string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total int64
	for _, record := range r.bytes {
		if record.operation == operation && record.value == direction {
			total += record.bytes
		}
	}
	return total
}

func (r *recordingTrafficRecorder) requestTotal(operation, result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total int
	for _, record := range r.requests {
		if record.operation == operation && record.value == result {
			total++
		}
	}
	return total
}

func TestWrapConnRecordsReadAndWriteBytes(t *testing.T) {
	recorder := &recordingTrafficRecorder{}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	metered := WrapConn(client, "metadata", recorder)

	writeDone := make(chan error, 1)
	go func() {
		_, err := server.Write([]byte("response"))
		writeDone <- err
	}()

	buf := make([]byte, len("response"))
	if _, err := io.ReadFull(metered, buf); err != nil {
		t.Fatalf("read metered connection: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write server connection: %v", err)
	}

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, len("request"))
		_, err := io.ReadFull(server, buf)
		readDone <- err
	}()

	if _, err := metered.Write([]byte("request")); err != nil {
		t.Fatalf("write metered connection: %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("read server connection: %v", err)
	}

	if got := recorder.byteTotal("metadata", TrafficDirectionReceived); got != int64(len("response")) {
		t.Errorf("received bytes = %d, want %d", got, len("response"))
	}
	if got := recorder.byteTotal("metadata", TrafficDirectionSent); got != int64(len("request")) {
		t.Errorf("sent bytes = %d, want %d", got, len("request"))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMeterRoundTripperRecordsStatusAndErrors(t *testing.T) {
	recorder := &recordingTrafficRecorder{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}

	success := MeterRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent}, nil
	}), "metadata", recorder)
	if _, err := success.RoundTrip(req); err != nil {
		t.Fatalf("successful round trip: %v", err)
	}

	failure := MeterRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	}), "metadata", recorder)
	if _, err := failure.RoundTrip(req); err == nil {
		t.Fatal("failed round trip returned nil error")
	}

	if got := recorder.requestTotal("metadata", "2xx"); got != 1 {
		t.Errorf("2xx requests = %d, want 1", got)
	}
	if got := recorder.requestTotal("metadata", TrafficResultTransportError); got != 1 {
		t.Errorf("transport errors = %d, want 1", got)
	}
}

func TestNewMeteredHTTPClientRecordsRedirectAttemptsAndSocketBytes(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/redirect" {
			http.Redirect(w, req, server.URL+"/payload", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "payload")
	}))
	defer server.Close()

	recorder := &recordingTrafficRecorder{}
	client := NewMeteredHTTPClient("metadata", recorder)

	resp, err := client.Get(server.URL + "/redirect")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}

	if got := recorder.requestTotal("metadata", "3xx"); got != 1 {
		t.Errorf("3xx requests = %d, want 1", got)
	}
	if got := recorder.requestTotal("metadata", "2xx"); got != 1 {
		t.Errorf("2xx requests = %d, want 1", got)
	}
	if got := recorder.byteTotal("metadata", TrafficDirectionSent); got == 0 {
		t.Error("sent bytes = 0, want a positive value")
	}
	if got := recorder.byteTotal("metadata", TrafficDirectionReceived); got <= int64(len("payload")) {
		t.Errorf("received bytes = %d, want more than the response body length", got)
	}
}
