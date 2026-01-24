package retry

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/rs/zerolog"
)

func ExponentialBackoff(ctx context.Context, maxRetries int, baseDelay time.Duration, operation func() error, logger zerolog.Logger, onRetry func(attempt int)) error {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
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
