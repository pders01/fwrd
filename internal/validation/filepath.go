package validation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FilePathValidator provides secure file path validation and sanitization
type FilePathValidator struct {
	// AllowedBaseDirs restricts file operations to specific base directories
	AllowedBaseDirs []string
	// AllowHomeExpansion determines if tilde expansion is permitted
	AllowHomeExpansion bool
	// AllowRelativePaths determines if relative paths are permitted
	AllowRelativePaths bool
	// MaxPathLength is the maximum allowed path length
	MaxPathLength int
}

// NewFilePathValidator creates a new validator with secure defaults
func NewFilePathValidator() *FilePathValidator {
	homeDir, _ := os.UserHomeDir()
	return &FilePathValidator{
		AllowedBaseDirs: []string{
			filepath.Join(homeDir, ".fwrd"),
			filepath.Join(homeDir, ".config", "fwrd"),
			os.TempDir(),
		},
		AllowHomeExpansion: true,
		AllowRelativePaths: false,
		MaxPathLength:      4096,
	}
}

// NewPermissiveFilePathValidator creates a validator for development/testing
func NewPermissiveFilePathValidator() *FilePathValidator {
	return &FilePathValidator{
		AllowedBaseDirs:    []string{}, // Empty means allow all directories
		AllowHomeExpansion: true,
		AllowRelativePaths: true,
		MaxPathLength:      4096,
	}
}

// ValidateAndSanitize validates and normalizes a file path
func (v *FilePathValidator) ValidateAndSanitize(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Length validation
	if len(path) > v.MaxPathLength {
		return "", fmt.Errorf("path too long (max %d characters)", v.MaxPathLength)
	}

	// Check for dangerous characters and sequences
	if err := v.validateCharacters(path); err != nil {
		return "", err
	}

	// Normalize the path
	normalizedPath, err := v.normalizePath(path)
	if err != nil {
		return "", fmt.Errorf("path normalization failed: %w", err)
	}

	// Validate against directory traversal
	if err := v.validateTraversal(normalizedPath); err != nil {
		return "", err
	}

	// Check if path is within allowed directories
	if err := v.validateBaseDirs(normalizedPath); err != nil {
		return "", err
	}

	return normalizedPath, nil
}

// validateCharacters checks for dangerous characters in the path
func (v *FilePathValidator) validateCharacters(path string) error {
	// Check for null bytes (directory traversal technique)
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null bytes")
	}

	// Check for control characters
	for _, char := range path {
		if char < 32 && char != '\t' {
			return fmt.Errorf("path contains control characters")
		}
	}

	// Check for dangerous sequences
	dangerous := []string{
		"../",  // Directory traversal
		"..\\", // Windows directory traversal
		"./",   // Current directory (potentially dangerous in some contexts)
		"//",   // Double slashes (UNC paths on Windows)
		"\\\\", // UNC paths
	}

	lowerPath := strings.ToLower(path)
	for _, seq := range dangerous {
		if strings.Contains(lowerPath, seq) {
			return fmt.Errorf("path contains dangerous sequence: %s", seq)
		}
	}

	return nil
}

// normalizePath normalizes the path by expanding home directory and making it absolute
func (v *FilePathValidator) normalizePath(path string) (string, error) {
	// Handle tilde expansion
	if v.AllowHomeExpansion && len(path) >= 2 && path[:2] == "~/" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(homeDir, path[2:])
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("tilde expansion not allowed or invalid tilde usage")
	}

	// Convert to absolute path if relative paths are not allowed
	if !v.AllowRelativePaths && !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("cannot make path absolute: %w", err)
		}
		path = absPath
	}

	// Clean the path to remove redundancies
	cleanPath := filepath.Clean(path)

	// Validate that cleaning didn't reveal traversal attempts
	if cleanPath != path && strings.Contains(path, "..") {
		return "", fmt.Errorf("path contains directory traversal after normalization")
	}

	return cleanPath, nil
}

// validateTraversal ensures the path doesn't attempt directory traversal
func (v *FilePathValidator) validateTraversal(path string) error {
	// Split path into components and check each
	components := strings.Split(filepath.ToSlash(path), "/")
	for _, component := range components {
		if component == ".." {
			return fmt.Errorf("directory traversal not allowed")
		}
		if component == "." && !v.AllowRelativePaths {
			return fmt.Errorf("relative path components not allowed")
		}
	}
	return nil
}

// validateBaseDirs ensures the path is within allowed base directories
func (v *FilePathValidator) validateBaseDirs(path string) error {
	// If no allowed base directories are specified, allow all paths
	if len(v.AllowedBaseDirs) == 0 {
		return nil
	}

	// Make path absolute for comparison
	absPath := path
	if !filepath.IsAbs(path) {
		var err error
		absPath, err = filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("cannot resolve absolute path: %w", err)
		}
	}

	// Check if path is within any allowed base directory
	for _, baseDir := range v.AllowedBaseDirs {
		absBaseDir, err := filepath.Abs(baseDir)
		if err != nil {
			continue // Skip invalid base directories
		}

		// Check if the path is within this base directory
		relPath, err := filepath.Rel(absBaseDir, absPath)
		if err != nil {
			continue
		}

		// If relative path doesn't start with "..", it's within the base directory
		if !strings.HasPrefix(relPath, "..") {
			return nil
		}
	}

	return fmt.Errorf("path not within allowed directories: %v", v.AllowedBaseDirs)
}

// ValidateDirectory ensures a directory path is safe and creates it if necessary
func (v *FilePathValidator) ValidateDirectory(path string, createIfNotExist bool) (string, error) {
	validatedPath, err := v.ValidateAndSanitize(path)
	if err != nil {
		return "", err
	}

	// Check if path exists
	info, err := os.Stat(validatedPath)
	if err != nil {
		if os.IsNotExist(err) {
			if createIfNotExist {
				// Create directory with secure permissions
				if mkErr := os.MkdirAll(validatedPath, 0o755); mkErr != nil {
					return "", fmt.Errorf("failed to create directory: %w", mkErr)
				}
			} else {
				// Directory doesn't exist and we're not creating it
				// This is OK for parent directory checks
				return validatedPath, nil
			}
		} else {
			return "", fmt.Errorf("checking directory: %w", err)
		}
	} else {
		// Path exists, verify it's a directory
		if !info.IsDir() {
			return "", fmt.Errorf("path exists but is not a directory: %s", validatedPath)
		}
	}

	return validatedPath, nil
}

// ValidateFile ensures a file path is safe for read/write operations
func (v *FilePathValidator) ValidateFile(path string) (string, error) {
	validatedPath, err := v.ValidateAndSanitize(path)
	if err != nil {
		return "", err
	}

	// Ensure parent directory is also within allowed paths
	parentDir := filepath.Dir(validatedPath)
	// Just validate the parent path is within allowed directories
	if err := v.validateBaseDirs(parentDir); err != nil {
		return "", fmt.Errorf("parent directory not allowed: %w", err)
	}

	// Check if file exists and is actually a file (not a directory)
	if info, err := os.Stat(validatedPath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("path is a directory, not a file: %s", validatedPath)
		}
	}

	return validatedPath, nil
}

// IsPathSafe performs a quick safety check on a path without full validation
func IsPathSafe(path string) bool {
	// Basic safety checks for common attacks
	if strings.Contains(path, "\x00") {
		return false
	}
	if strings.Contains(path, "../") || strings.Contains(path, "..\\") {
		return false
	}
	if len(path) > 4096 {
		return false
	}
	return true
}
