package debuglog

import (
	"log"
	"os"
	"path/filepath"
)

var (
	enabled bool
	logger  *log.Logger
)

// Setup enables debug logging to ~/.fwrd/fwrd.log when on=true.
func Setup(on bool) {
	enabled = on
	if !enabled {
		logger = nil
		return
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".fwrd")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "fwrd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		enabled = false
		logger = nil
		return
	}
	logger = log.New(f, "fwrd ", log.LstdFlags)
}

func Infof(format string, args ...any) {
	if enabled && logger != nil {
		logger.Printf("INFO "+format, args...)
	}
}

func Errorf(format string, args ...any) {
	if enabled && logger != nil {
		logger.Printf("ERROR "+format, args...)
	}
}
