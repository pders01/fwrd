package tui

import (
	"errors"
	"fmt"
)

// setOperationError clears only the error owned by a recovered operation.
func (a *App) setOperationError(owner, err error) error {
	if err != nil {
		a.err = err
	} else if owner != nil && errors.Is(a.err, owner) {
		a.err = nil
	}
	return err
}

// wrapErr formats an error with a contextual prefix.
func wrapErr(context string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}
