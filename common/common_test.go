package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryForever(t *testing.T) {
	t.Parallel()

	t.Run("retries an error that wraps an inner deadline", func(t *testing.T) {
		t.Parallel()

		calls := 0

		RetryForever(context.Background(), time.Millisecond, func(context.Context) error {
			calls++
			if calls < 3 {
				return fmt.Errorf("inner call: %w", context.DeadlineExceeded)
			}

			return nil
		})

		require.Equal(t, 3, calls)
	})

	t.Run("stops when its own context is cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		calls := 0

		RetryForever(ctx, time.Millisecond, func(context.Context) error {
			calls++

			cancel()

			return errors.New("still failing")
		})

		require.Equal(t, 1, calls)
	})
}
