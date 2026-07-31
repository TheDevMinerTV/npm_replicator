package internal

import (
	"net"
	"net/http"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

// MeterProxyTransport decorates each concrete transport behind the
// round-robin proxy. Decorating the inner transports keeps proxy selection and
// cooldown behavior independent while still counting every retry attempt.
func MeterProxyTransport(
	proxy *roundRobinTransport,
	operation string,
	recorder httpclient.TrafficRecorder,
) http.RoundTripper {
	if recorder == nil {
		return proxy
	}

	for _, state := range proxy.proxies {
		state.transport = meterTransport(state.transport, operation, recorder)
	}

	return proxy
}

func meterTransport(
	base http.RoundTripper,
	operation string,
	recorder httpclient.TrafficRecorder,
) http.RoundTripper {
	if transport, ok := base.(*http.Transport); ok {
		transport = transport.Clone()

		dialContext := transport.DialContext
		if dialContext == nil {
			dialer := &net.Dialer{}
			dialContext = dialer.DialContext
		}
		transport.DialContext = httpclient.MeterDialContext(dialContext, operation, recorder)
		base = transport
	}

	return httpclient.MeterRoundTripper(base, operation, recorder)
}
