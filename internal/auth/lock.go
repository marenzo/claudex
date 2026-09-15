package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The lock lives beside the credential because atomic replacement changes the
// credential's inode. Keep this file in place: unlinking it would split ownership
// between existing and newly opened lock descriptors. Claudex supports Unix hosts.
func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return nil, fmt.Errorf("prepare credential lock: %w", err)
	}
	file, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open credential lock: %w", err)
	}
	closeFile := func() {
		if err := file.Close(); err != nil {
			slog.Debug("credential lock close failed")
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			closeFile()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
					slog.Debug("credential unlock failed")
				}
				closeFile()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			closeFile()
			return nil, fmt.Errorf("lock credential: %w", err)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			closeFile()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
