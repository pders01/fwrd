package validation

import (
	"os"
	"path/filepath"
)

// PathHandler provides secure path operations with validation
type PathHandler struct {
	validator *FilePathValidator
}

// NewSecurePathHandler creates a path handler with secure validation
func NewSecurePathHandler() *PathHandler {
	return &PathHandler{
		validator: NewFilePathValidator(),
	}
}

// NewPermissivePathHandler creates a path handler for development/testing
func NewPermissivePathHandler() *PathHandler {
	return &PathHandler{
		validator: NewPermissiveFilePathValidator(),
	}
}

// ExpandAndValidatePath safely expands and validates a path
func (ph *PathHandler) ExpandAndValidatePath(path string) (string, error) {
	return ph.validator.ValidateAndSanitize(path)
}

// GetSecureDBPath returns a validated database path
func (ph *PathHandler) GetSecureDBPath(userPath string) (string, error) {
	if userPath == "" {
		// Default secure location
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		userPath = filepath.Join(homeDir, ".fwrd.db")
	}

	return ph.validator.ValidateFile(userPath)
}

// GetSecureConfigPath returns a validated configuration path
func (ph *PathHandler) GetSecureConfigPath(userPath string) (string, error) {
	if userPath == "" {
		// Default secure location
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		userPath = filepath.Join(homeDir, ".config", "fwrd", "config.toml")
	}

	return ph.validator.ValidateFile(userPath)
}

// GetSecureIndexPath returns a validated search index path
func (ph *PathHandler) GetSecureIndexPath(userPath string) (string, error) {
	if userPath == "" {
		// Default secure location
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		userPath = filepath.Join(homeDir, ".fwrd", "index.bleve")
	}

	// Validate as directory since bleve indexes are directories
	return ph.validator.ValidateDirectory(userPath, false)
}

// EnsureSecureDirectory creates a directory safely after validation
func (ph *PathHandler) EnsureSecureDirectory(path string) (string, error) {
	return ph.validator.ValidateDirectory(path, true)
}
