package humanize

import (
	"context"
	"time"
)

func Delay(ctx context.Context, action Action) error {
	dist, ok := defaultProvider.Timing()[action]
	if !ok {
		dist = defaultProvider.Timing()[AfterClick]
	}

	t := time.NewTimer(dist.Sample())
	defer t.Stop()

	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
