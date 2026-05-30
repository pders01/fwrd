//go:build !linux && !darwin

package service

import "errors"

// errUnsupported is returned on platforms without a supported init system.
var errUnsupported = errors.New("fwrd service is only supported on Linux (systemd) and macOS (launchd)")

func Install(*Options) (string, error) { return "", errUnsupported }

func Uninstall() (string, error) { return "", errUnsupported }

func LogCommand(bool, int) (name string, args []string, err error) { return "", nil, errUnsupported }
