package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
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
	DefaultMaxRetries = 3
	DefaultBaseDelay  = 1 * time.Second
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
		return callWebhookOnce(ctx, client, url, payload, log)
	}

	onRetry := func(attempt int) {
		WebhookRetriesTotal.WithLabelValues(url).Inc()
	}

	err := retryWithExponentialBackoff(ctx, DefaultMaxRetries, DefaultBaseDelay, operation, log, onRetry)
	if err != nil {
		log.Error().Err(err).Msg("Webhook call failed after all retries")
		WebhookCallsTotal.WithLabelValues("failure", url).Inc()
		return
	}

	log.Info().Msg("Webhook call succeeded")
	WebhookCallsTotal.WithLabelValues("success", url).Inc()
}

func callWebhookOnce(ctx context.Context, client *httpclient.Client, url string, payload []byte, log zerolog.Logger) error {
	headers := http.Header{"Content-Type": []string{"application/json"}}
	if token, ok := WebhookAuthorizations[url]; ok {
		log.Debug().Msg("Adding authorization header to webhook request")
		headers["Authorization"] = []string{"Bearer " + token}
	}
	_, err := client.Post(ctx, url, nil, headers, bytes.NewReader(payload), func(code int) bool { return code >= 200 && code < 300 })
	return err
}

func retryWithExponentialBackoff(ctx context.Context, maxRetries int, baseDelay time.Duration, operation func() error, logger zerolog.Logger, onRetry func(attempt int)) error {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff with jitter
			delay := baseDelay * time.Duration(1<<uint(attempt-1))                // 2^(attempt-1)
			jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5)) // ±25% jitter

			logger.Debug().Int("attempt", attempt).Dur("delay", jitter).Msg("Retrying after delay")
			select {
			case <-ctx.Done():
				logger.Warn().Msg("Operation canceled due to context done")
				return ctx.Err()
			case <-time.After(jitter):
			}
			if onRetry != nil {
				onRetry(attempt)
			}
		}

		err := operation()
		if err == nil {
			return nil
		}

		logger.Warn().Err(err).Int("attempt", attempt+1).Msg("Operation failed")
	}

	return fmt.Errorf("operation failed after %d retries", maxRetries)
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
		resp, err := client.GetJSON(ctx, endpoint, nil, nil, func(code int) bool { return code == 200 })
		if err != nil {
			return err
		}

		return json.NewDecoder(resp).Decode(&newPackages)
	}

	onRetry := func(attempt int) {
		WebhookEndpointRetriesTotal.WithLabelValues(endpoint).Inc()
	}

	err := retryWithExponentialBackoff(ctx, DefaultMaxRetries, DefaultBaseDelay, operation, logger, onRetry)
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
