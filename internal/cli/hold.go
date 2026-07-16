package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// holdOpen keeps the process alive until interrupt/term.
// If onTick is set, it runs every interval (lease renew).
func holdOpen(ctx context.Context, onTick func() error, every time.Duration) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if onTick == nil {
		<-ctx.Done()
		return nil
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := onTick(); err != nil {
				return err
			}
		}
	}
}
