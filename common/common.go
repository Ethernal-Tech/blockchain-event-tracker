package common

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/sethvargo/go-retry"
)

// EncodeUint64ToBytes encodes provided uint64 to big endian byte slice
func EncodeUint64ToBytes(value uint64) []byte {
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, value)

	return result
}

// EncodeBytesToUint64 big endian byte slice to uint64
func EncodeBytesToUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

// RetryForever will execute a function until it completes without error or
// the context is cancelled or expired.
func RetryForever(ctx context.Context, interval time.Duration, fn func(context.Context) error) {
	_ = retry.Do(ctx, retry.NewConstant(interval), func(context.Context) error {
		// Execute function and end retries if no error or context done
		err := fn(ctx)
		if err == nil || IsContextDone(err) {
			return nil
		}

		// Retry on all other errors
		return retry.RetryableError(err)
	})
}

// IsContextDone returns true if the error is due to the context being cancelled
// or expired. This is useful for determining if a function should retry.
func IsContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
