package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSecurePathHandler(t *testing.T) {
	ph := NewSecurePathHandler()
	if ph == nil {
		t.Fatal("NewSecurePathHandler returned nil")
	}
	if ph.validator == nil {
		t.Fatal("PathHandler validator is nil")
	}

	// Should use secure defaults
	if ph.validator.AllowRelativePaths {
		t.Error("Expected secure path handler to disallow relative paths")
	}
	if !ph.validator.AllowHomeExpansion {
		t.Error("Expected secure path handler to allow home expansion")
	}
}

func TestNewPermissivePathHandler(t *testing.T) {
	ph := NewPermissivePathHandler()
	if ph == nil {
		t.Fatal("NewPermissivePathHandler returned nil")
	}
	if ph.validator == nil {
		t.Fatal("PathHandler validator is nil")
	}

	// Should use permissive settings
	if !ph.validator.AllowRelativePaths {
		t.Error("Expected permissive path handler to allow relative paths")
	}
	if len(ph.validator.AllowedBaseDirs) != 0 {
		t.Error("Expected permissive path handler to have no base directory restrictions")
	}
}

func TestExpandAndValidatePath(t *testing.T) {
	ph := NewPermissivePathHandler()
	tempDir := os.TempDir()

	tests := []struct {
		name        string
		input       string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "valid absolute path",
			input:       filepath.Join(tempDir, "test.txt"),
			shouldError: false,
		},
		{
			name:        "valid relative path with permissive handler",
			input:       "test.txt",
			shouldError: false,
		},
		{
			name:        "empty path",
			input:       "",
			shouldError: true,
			errorMsg:    "path cannot be empty",
		},
		{
			name:        "path with null byte",
			input:       "/tmp/test\x00.txt",
			shouldError: true,
			errorMsg:    "null bytes",
		},
		{
			name:        "directory traversal",
			input:       "../../../etc/passwd",
			shouldError: true,
			errorMsg:    "dangerous sequence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ph.ExpandAndValidatePath(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result == "" {
					t.Error("Expected non-empty result for valid input")
				}
			}
		})
	}
}

func TestGetSecureDBPath(t *testing.T) {
	ph := NewPermissivePathHandler() // Use permissive to avoid base dir restrictions
	tempDir := os.TempDir()

	tests := []struct {
		name          string
		input         string
		shouldError   bool
		expectDefault bool
	}{
		{
			name:          "empty path uses default",
			input:         "",
			shouldError:   false,
			expectDefault: true,
		},
		{
			name:        "custom valid path",
			input:       filepath.Join(tempDir, "custom.db"),
			shouldError: false,
		},
		{
			name:        "invalid path with null byte",
			input:       "/tmp/test\x00.db",
			shouldError: true,
		},
		{
			name:        "directory traversal attack",
			input:       "../../../etc/passwd",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ph.GetSecureDBPath(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result == "" {
					t.Error("Expected non-empty result for valid input")
				}
				if tt.expectDefault {
					homeDir, _ := os.UserHomeDir()
					expectedDefault := filepath.Join(homeDir, ".fwrd", "fwrd.db")
					if result != expectedDefault {
						t.Errorf("Expected default path %q, got %q", expectedDefault, result)
					}
				}
			}
		})
	}
}

func TestGetSecureConfigPath(t *testing.T) {
	ph := NewPermissivePathHandler()
	tempDir := os.TempDir()

	tests := []struct {
		name          string
		input         string
		shouldError   bool
		expectDefault bool
	}{
		{
			name:          "empty path uses default",
			input:         "",
			shouldError:   false,
			expectDefault: true,
		},
		{
			name:        "custom valid path",
			input:       filepath.Join(tempDir, "custom.toml"),
			shouldError: false,
		},
		{
			name:        "invalid path with control character",
			input:       "/tmp/config\x01.toml",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ph.GetSecureConfigPath(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result == "" {
					t.Error("Expected non-empty result for valid input")
				}
				if tt.expectDefault {
					homeDir, _ := os.UserHomeDir()
					expectedDefault := filepath.Join(homeDir, ".config", "fwrd", "config.toml")
					if result != expectedDefault {
						t.Errorf("Expected default path %q, got %q", expectedDefault, result)
					}
				}
			}
		})
	}
}

func TestGetSecureIndexPath(t *testing.T) {
	ph := NewPermissivePathHandler()
	tempDir := os.TempDir()

	tests := []struct {
		name          string
		input         string
		shouldError   bool
		expectDefault bool
	}{
		{
			name:          "empty path uses default",
			input:         "",
			shouldError:   false,
			expectDefault: true,
		},
		{
			name:        "custom valid path",
			input:       filepath.Join(tempDir, "custom.bleve"),
			shouldError: false,
		},
		{
			name:        "invalid path with dangerous sequence",
			input:       "../../index.bleve",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ph.GetSecureIndexPath(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result == "" {
					t.Error("Expected non-empty result for valid input")
				}
				if tt.expectDefault {
					homeDir, _ := os.UserHomeDir()
					expectedDefault := filepath.Join(homeDir, ".fwrd", "index.bleve")
					if result != expectedDefault {
						t.Errorf("Expected default path %q, got %q", expectedDefault, result)
					}
				}
			}
		})
	}
}

func TestEnsureSecureDirectory(t *testing.T) {
	ph := NewPermissivePathHandler()
	tempDir := os.TempDir()

	// Create a test directory path that doesn't exist
	testDir := filepath.Join(tempDir, "test_ensure_dir")
	// Clean up before test
	os.RemoveAll(testDir)

	tests := []struct {
		name         string
		path         string
		shouldError  bool
		shouldCreate bool
		cleanup      bool
	}{
		{
			name:        "existing directory",
			path:        tempDir,
			shouldError: false,
		},
		{
			name:         "non-existent directory should be created",
			path:         testDir,
			shouldError:  false,
			shouldCreate: true,
			cleanup:      true,
		},
		{
			name:        "invalid path with null byte",
			path:        "/tmp/test\x00dir",
			shouldError: true,
		},
		{
			name:        "directory traversal attack",
			path:        "../../../tmp/attack",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up before test if needed
			if tt.cleanup {
				os.RemoveAll(tt.path)
			}

			result, err := ph.EnsureSecureDirectory(tt.path)

			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for path %q", tt.path)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %q: %v", tt.path, err)
				}
				if result == "" {
					t.Error("Expected non-empty result for valid input")
				}

				// Check if directory was created
				if tt.shouldCreate {
					if _, err := os.Stat(tt.path); os.IsNotExist(err) {
						t.Errorf("Directory was not created: %s", tt.path)
					}
				}
			}

			// Clean up after test if needed
			if tt.cleanup && !tt.shouldError {
				os.RemoveAll(tt.path)
			}
		})
	}
}

func TestPathHandlerWithSecureValidator(t *testing.T) {
	ph := NewSecurePathHandler()

	// Test that secure validator restricts paths outside allowed directories
	_, err := ph.ExpandAndValidatePath("/etc/passwd")
	if err == nil {
		t.Error("Expected secure path handler to reject path outside allowed directories")
	}

	// Test that home directory expansion works
	homeDir, _ := os.UserHomeDir()
	result, err := ph.ExpandAndValidatePath("~/.fwrd/test.db")
	if err != nil {
		t.Errorf("Expected secure path handler to allow home expansion: %v", err)
	}
	expectedPath := filepath.Join(homeDir, ".fwrd", "test.db")
	if result != expectedPath {
		t.Errorf("Expected %q, got %q", expectedPath, result)
	}
}

func TestPathHandlerEdgeCases(t *testing.T) {
	ph := NewPermissivePathHandler()

	// Test with extremely long path
	longPath := strings.Repeat("a", 5000)
	_, err := ph.ExpandAndValidatePath(longPath)
	if err == nil {
		t.Error("Expected error for extremely long path")
	}

	// Test with path containing only spaces - this might be valid as it's not empty
	// The validator doesn't trim before validation, so "   " is not empty
	result, err := ph.ExpandAndValidatePath("   ")
	if err != nil {
		t.Logf("Whitespace-only path rejected (expected): %v", err)
	} else {
		t.Logf("Whitespace-only path accepted, result: %q", result)
	}

	// Test home expansion with permissive validator
	homeDir, _ := os.UserHomeDir()
	result2, err := ph.ExpandAndValidatePath("~/test.txt")
	if err != nil {
		t.Errorf("Unexpected error with home expansion: %v", err)
	}
	expectedPath := filepath.Join(homeDir, "test.txt")
	if result2 != expectedPath {
		t.Errorf("Expected %q, got %q", expectedPath, result2)
	}
}

func TestDefaultPaths(t *testing.T) {
	ph := NewPermissivePathHandler()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	// Test default database path
	dbPath, err := ph.GetSecureDBPath("")
	if err != nil {
		t.Errorf("Error getting default DB path: %v", err)
	}
	expectedDB := filepath.Join(homeDir, ".fwrd", "fwrd.db")
	if dbPath != expectedDB {
		t.Errorf("Expected default DB path %q, got %q", expectedDB, dbPath)
	}

	// Test default config path
	configPath, err := ph.GetSecureConfigPath("")
	if err != nil {
		t.Errorf("Error getting default config path: %v", err)
	}
	expectedConfig := filepath.Join(homeDir, ".config", "fwrd", "config.toml")
	if configPath != expectedConfig {
		t.Errorf("Expected default config path %q, got %q", expectedConfig, configPath)
	}

	// Test default index path
	indexPath, err := ph.GetSecureIndexPath("")
	if err != nil {
		t.Errorf("Error getting default index path: %v", err)
	}
	expectedIndex := filepath.Join(homeDir, ".fwrd", "index.bleve")
	if indexPath != expectedIndex {
		t.Errorf("Expected default index path %q, got %q", expectedIndex, indexPath)
	}
}
