package internal

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	xproxy "golang.org/x/net/proxy"
)

func LoadProxyList(path string, cooldown time.Duration, disableOfflineMarking bool, errorCounter *prometheus.CounterVec) (*roundRobinTransport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var proxies []*url.URL
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", line, err)
		}
		proxies = append(proxies, u)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(proxies) == 0 {
		return nil, fmt.Errorf("no proxies found in %s", path)
	}

	log.Info().Int("count", len(proxies)).Msg("Loaded download proxies")

	proxyStates, err := buildProxyTransports(proxies)
	if err != nil {
		return nil, err
	}

	return &roundRobinTransport{
		proxies:               proxyStates,
		cooldown:              cooldown,
		disableOfflineMarking: disableOfflineMarking,
		errorCounter:          errorCounter,
	}, nil
}

type proxyState struct {
	transport http.RoundTripper
	name      string
	downUntil atomic.Int64 // unix timestamp; 0 = healthy
}

type roundRobinTransport struct {
	proxies  []*proxyState
	idx      atomic.Uint64
	cooldown time.Duration
	// disableOfflineMarking keeps failing proxies in rotation instead of putting
	// them on cooldown. Useful when the list points at a single load balancer
	// that fans out to many upstream proxies itself.
	disableOfflineMarking bool
	errorCounter          *prometheus.CounterVec
}

func (rr *roundRobinTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := uint64(len(rr.proxies))
	start := rr.idx.Add(1) - 1

	var lastErr error
	for offset := range n {
		ps := rr.proxies[(start+offset)%n]

		if !rr.disableOfflineMarking {
			if downUntil := ps.downUntil.Load(); downUntil != 0 {
				if time.Now().Unix() < downUntil {
					continue // still in cooldown
				}
				ps.downUntil.Store(0) // cooldown expired, mark healthy
			}
		}

		resp, err := ps.transport.RoundTrip(req)
		if err != nil {
			lastErr = err
			rr.errorCounter.With(prometheus.Labels{"proxy": ps.name}).Inc()
			if rr.disableOfflineMarking {
				log.Warn().Err(err).Str("proxy", ps.name).Msg("proxy error")
			} else {
				ps.downUntil.Store(time.Now().Add(rr.cooldown).Unix())
				log.Warn().Err(err).Str("proxy", ps.name).Dur("cooldown", rr.cooldown).Msg("proxy error, marking down")
			}
			continue
		}

		return resp, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all proxies failed: %w", lastErr)
	}

	return nil, fmt.Errorf("all proxies are down")
}

func buildProxyTransports(proxies []*url.URL) ([]*proxyState, error) {
	states := make([]*proxyState, 0, len(proxies))

	for _, p := range proxies {
		var transport http.RoundTripper

		switch p.Scheme {
		case "socks5":
			var auth *xproxy.Auth
			if p.User != nil {
				password, _ := p.User.Password()
				auth = &xproxy.Auth{
					User:     p.User.Username(),
					Password: password,
				}
			}
			d, err := xproxy.SOCKS5("tcp", p.Host, auth, xproxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("failed to create SOCKS5 dialer for %s: %w", p.Host, err)
			}
			transport = &http.Transport{
				DialContext: d.(xproxy.ContextDialer).DialContext,
			}

		case "http", "https":
			proxyURL := *p // copy so the closure is safe
			transport = &http.Transport{
				Proxy: http.ProxyURL(&proxyURL),
			}

		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q in %s", p.Scheme, p.String())
		}

		states = append(states, &proxyState{
			transport: transport,
			name:      p.String(),
		})
	}

	return states, nil
}
