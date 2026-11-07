//go:build windows

package ipc

import (
	"context"
	"fmt"
	"testing"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestWindowsNativePipeFailuresAreRetryable(t *testing.T) {
	for _, err := range []error{winio.ErrFileClosed, windows.ERROR_BROKEN_PIPE, windows.ERROR_NO_DATA, windows.ERROR_PIPE_NOT_CONNECTED} {
		if !IsRetryable(fmt.Errorf("write request: write frame: %w", err)) {
			t.Errorf("native pipe failure not classified: %v", err)
		}
	}
	for _, err := range []error{winio.ErrTimeout, windows.ERROR_ACCESS_DENIED, context.Canceled, context.DeadlineExceeded, ErrProtocol, ErrDatabaseMismatch} {
		if IsRetryable(fmt.Errorf("request: %w", err)) {
			t.Errorf("non-transport failure classified as retryable: %v", err)
		}
	}
}
