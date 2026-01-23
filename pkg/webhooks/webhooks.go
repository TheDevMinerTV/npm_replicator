package webhooks

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

type WebhookMap map[string][]string

var PackageWebhooks = WebhookMap{}

var (
	WebhookCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "calls_total",
			Help:      "Total number of webhook calls",
		},
		[]string{"status", "url"},
	)
	WebhookRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "webhooks",
			Name:      "retries_total",
			Help:      "Total number of webhook retries",
		},
		[]string{"url"},
	)
)

func CallWebhooksAsync(ctx context.Context, packageName string, payload []byte) {
	webhookURLs, exists := PackageWebhooks[packageName]
	if !exists || len(webhookURLs) == 0 {
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
	logger := log.With().Str("webhook_url", url).Str("package", packageName).Logger()

	const maxRetries = 3
	backoffDelays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelays[attempt-1]
			logger.Debug().Int("attempt", attempt).Dur("delay", delay).Msg("Retrying webhook after delay")
			select {
			case <-ctx.Done():
				logger.Warn().Msg("Webhook call canceled due to context done")
				return
			case <-time.After(delay):
			}
		}

		err := callWebhookOnce(ctx, client, url, payload)
		if err == nil {
			logger.Info().Int("attempt", attempt+1).Msg("Webhook call succeeded")
			WebhookCallsTotal.WithLabelValues("success", url).Inc()
			return
		}

		logger.Warn().Err(err).Int("attempt", attempt+1).Msg("Webhook call failed")

		if attempt < maxRetries {
			WebhookRetriesTotal.WithLabelValues(url).Inc()
		}

		if attempt == maxRetries {
			logger.Error().Err(err).Msg("Webhook call failed after all retries")
			WebhookCallsTotal.WithLabelValues("failure", url).Inc()
		}
	}
}

func callWebhookOnce(ctx context.Context, client *httpclient.Client, url string, payload []byte) error {
	headers := http.Header{"Content-Type": []string{"application/json"}}
	_, err := client.Post(ctx, url, nil, headers, bytes.NewReader(payload), func(code int) bool { return code >= 200 && code < 300 })
	return err
}
