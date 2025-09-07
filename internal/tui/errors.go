package tui

import "fmt"

// wrapErr formats an error with a contextual prefix.
func wrapErr(context string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}
