package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
	"github.com/thedevminertv/npm-replicator/pkg/retry"
)

// webhook URL <-> package names
var Endpoints = xsync.NewMapOf[string, map[string]bool]()

var WebhookListenerEndpoints = []string{
	"https://svelte-changelog.dev/api/webhooks/packages",
}

var (
	WebhookCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "calls_total",
			Help:      "Total number of webhook calls",
		},
		[]string{"status", "endpoint"},
	)
	WebhookRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "retries_total",
			Help:      "Total number of webhook retries",
		},
		[]string{"endpoint"},
	)
	WebhookEndpointRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "endpoint_retries_total",
			Help:      "Total number of endpoint update retries",
		},
		[]string{"endpoint"},
	)
	WebhookSubscribedPackages = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "subscribed_packages",
			Help:      "Number of packages subscribed per endpoint",
		},
		[]string{"endpoint"},
	)

	WebhookAuthorizations map[string]string
)

const (
	DefaultMaxRetries = 5
	DefaultBaseDelay  = 5 * time.Second
)

func CallWebhooksAsync(ctx context.Context, packageName string, payload []byte) {
	webhookURLs := make([]string, 0)
	Endpoints.Range(func(endpoint string, packages map[string]bool) bool {
		if packages[packageName] {
			webhookURLs = append(webhookURLs, endpoint)
		}
		return true
	})
	if len(webhookURLs) == 0 {
		log.Trace().Str("package", packageName).Msg("No webhooks configured for package")
		return
	}

	log.Info().Str("package", packageName).Int("webhook_count", len(webhookURLs)).Msg("Sending webhook notifications")

	client := httpclient.New("", httpclient.WithCustomClient(&http.Client{Timeout: 30 * time.Second}))

	for _, url := range webhookURLs {
		go callWebhookWithRetry(ctx, client, url, payload, packageName)
	}
}

func callWebhookWithRetry(ctx context.Context, client *httpclient.Client, url string, payload []byte, packageName string) {
	log := log.With().Str("webhook_url", url).Str("package", packageName).Logger()

	operation := func() error {
		headers := http.Header{"Content-Type": []string{"application/json"}}
		if token, ok := WebhookAuthorizations[url]; ok {
			log.Debug().Msg("Adding authorization header to webhook request")
			headers["Authorization"] = []string{"Bearer " + token}
		}
		_, err := client.Post(ctx, url, nil, headers, bytes.NewReader(payload), httpclient.AllSuccessful)
		if err != nil {
			var httpErr httpclient.UnexpectedHTTPStatusCodeError
			if ok := errors.As(err, &httpErr); ok && httpclient.AllClientErrors(httpErr.StatusCode) {
				return &retry.NonRetryableError{Err: err}
			}
		}
		return err
	}

	onRetry := func(attempt int) {
		WebhookRetriesTotal.WithLabelValues(url).Inc()
	}

	err := retry.ExponentialBackoff(ctx, DefaultMaxRetries, DefaultBaseDelay, operation, log, onRetry)
	if err != nil {
		log.Error().Err(err).Msg("Webhook call failed after all retries")
		WebhookCallsTotal.WithLabelValues("failure", url).Inc()
		return
	}

	log.Info().Msg("Webhook call succeeded")
	WebhookCallsTotal.WithLabelValues("success", url).Inc()
}

func RefreshWebhookListeners(ctx context.Context) {
	for _, endpoint := range WebhookListenerEndpoints {
		updateEndpointListeners(ctx, endpoint)
	}
}

func updateEndpointListeners(ctx context.Context, endpoint string) {
	logger := log.With().Str("endpoint", endpoint).Logger()

	_, _ = Endpoints.Load(endpoint)

	client := httpclient.New("", httpclient.WithCustomClient(&http.Client{Timeout: 30 * time.Second}))
	var newPackages []string

	operation := func() error {
		resp, err := client.GetJSON(ctx, endpoint, nil, nil, httpclient.ExactStatusCode(200))
		if err != nil {
			var httpErr httpclient.UnexpectedHTTPStatusCodeError
			if ok := errors.As(err, &httpErr); ok && httpclient.AllClientErrors(httpErr.StatusCode) {
				return &retry.NonRetryableError{Err: err}
			}
			return err
		}

		return json.NewDecoder(resp).Decode(&newPackages)
	}

	onRetry := func(attempt int) {
		WebhookEndpointRetriesTotal.WithLabelValues(endpoint).Inc()
	}

	err := retry.ExponentialBackoff(ctx, DefaultMaxRetries, DefaultBaseDelay, operation, logger, onRetry)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch packages from endpoint after all retries")
		logger.Warn().Msg("Keeping previous packages for endpoint due to fetch failure")
		return
	}

	logger.Info().Int("package_count", len(newPackages)).Msg("Successfully fetched packages from endpoint")

	packagesMap := make(map[string]bool)
	for _, pkg := range newPackages {
		packagesMap[pkg] = true
	}
	Endpoints.Store(endpoint, packagesMap)
	WebhookSubscribedPackages.WithLabelValues(endpoint).Set(float64(len(newPackages)))
}
