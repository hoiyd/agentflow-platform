package processfixture

import (
	"context"
	"fmt"
	"os"
	"time"
)

// WaitForFile waits for a marker written by the actual CLI, not a guessed delay.
func WaitForFile(ctx context.Context, path string) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("CLI did not reach the expected phase: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
