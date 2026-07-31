package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

const (
	TrafficDirectionReceived = "received"
	TrafficDirectionSent     = "sent"

	TrafficResultTransportError = "transport_error"
	TrafficResultOther          = "other"
)

// TrafficRecorder records socket bytes and HTTP request attempts for a
// low-cardinality operation name.
type TrafficRecorder interface {
	AddWireBytes(operation, direction string, bytes int64)
	IncRequest(operation, result string)
}

// WrapConn records bytes read from and written to conn. Since the wrapper sits
// below net/http's TLS handling, HTTPS traffic is counted as encrypted socket
// bytes, including TLS framing.
func WrapConn(conn net.Conn, operation string, recorder TrafficRecorder) net.Conn {
	if recorder == nil {
		return conn
	}

	return &trafficConn{
		Conn:      conn,
		operation: operation,
		recorder:  recorder,
	}
}

type trafficConn struct {
	net.Conn

	operation string
	recorder  TrafficRecorder
}

func (c *trafficConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.recorder.AddWireBytes(c.operation, TrafficDirectionReceived, int64(n))
	}
	return n, err
}

func (c *trafficConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.recorder.AddWireBytes(c.operation, TrafficDirectionSent, int64(n))
	}
	return n, err
}

// MeterDialContext wraps every connection returned by dial with socket-byte
// accounting.
func MeterDialContext(
	dial func(context.Context, string, string) (net.Conn, error),
	operation string,
	recorder TrafficRecorder,
) func(context.Context, string, string) (net.Conn, error) {
	if recorder == nil {
		return dial
	}

	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return WrapConn(conn, operation, recorder), nil
	}
}

// MeterRoundTripper counts each HTTP transport attempt. Placing this around
// the concrete transport means redirects and proxy retries are counted as
// separate requests.
func MeterRoundTripper(base http.RoundTripper, operation string, recorder TrafficRecorder) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if recorder == nil {
		return base
	}

	return &trafficRoundTripper{
		base:      base,
		operation: operation,
		recorder:  recorder,
	}
}

type trafficRoundTripper struct {
	base http.RoundTripper

	operation string
	recorder  TrafficRecorder
}

func (t *trafficRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.recorder.IncRequest(t.operation, TrafficResultTransportError)
		return nil, err
	}

	t.recorder.IncRequest(t.operation, statusFamily(resp.StatusCode))
	return resp, nil
}

func statusFamily(statusCode int) string {
	if statusCode < 100 || statusCode >= 600 {
		return TrafficResultOther
	}
	return fmt.Sprintf("%dxx", statusCode/100)
}

// NewMeteredHTTPClient returns an HTTP client with its own connection pool and
// socket/request accounting. A distinct pool keeps byte attribution reliable
// when multiple operations run concurrently or use HTTP/2.
func NewMeteredHTTPClient(operation string, recorder TrafficRecorder) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = MeterDialContext(transport.DialContext, operation, recorder)

	return &http.Client{
		Transport: MeterRoundTripper(transport, operation, recorder),
	}
}
