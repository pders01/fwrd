package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFilePathValidator(t *testing.T) {
	v := NewFilePathValidator()
	if v == nil {
		t.Fatal("NewFilePathValidator returned nil")
	}

	// Check secure defaults
	if !v.AllowHomeExpansion {
		t.Error("Expected AllowHomeExpansion to be true")
	}
	if v.AllowRelativePaths {
		t.Error("Expected AllowRelativePaths to be false for security")
	}
	if v.MaxPathLength != 4096 {
		t.Errorf("Expected MaxPathLength to be 4096, got %d", v.MaxPathLength)
	}
	if len(v.AllowedBaseDirs) == 0 {
		t.Error("Expected AllowedBaseDirs to be populated with secure defaults")
	}
}

func TestNewPermissiveFilePathValidator(t *testing.T) {
	v := NewPermissiveFilePathValidator()
	if v == nil {
		t.Fatal("NewPermissiveFilePathValidator returned nil")
	}

	// Check permissive settings
	if !v.AllowHomeExpansion {
		t.Error("Expected AllowHomeExpansion to be true")
	}
	if !v.AllowRelativePaths {
		t.Error("Expected AllowRelativePaths to be true for permissive mode")
	}
	if len(v.AllowedBaseDirs) != 0 {
		t.Error("Expected AllowedBaseDirs to be empty for permissive mode")
	}
}

func TestValidateAndSanitize(t *testing.T) {
	v := NewFilePathValidator()

	tests := []struct {
		name        string
		input       string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "empty path",
			input:       "",
			shouldError: true,
			errorMsg:    "path cannot be empty",
		},
		{
			name:        "path too long",
			input:       strings.Repeat("a", 5000),
			shouldError: true,
			errorMsg:    "path too long",
		},
		{
			name:        "null byte injection",
			input:       "/tmp/test\x00.txt",
			shouldError: true,
			errorMsg:    "null bytes",
		},
		{
			name:        "control characters",
			input:       "/tmp/test\x01.txt",
			shouldError: true,
			errorMsg:    "control characters",
		},
		{
			name:        "directory traversal",
			input:       "/tmp/../etc/passwd",
			shouldError: true,
			errorMsg:    "dangerous sequence",
		},
		{
			name:        "windows directory traversal",
			input:       "/tmp/..\\windows",
			shouldError: true,
			errorMsg:    "dangerous sequence",
		},
		{
			name:        "double slashes",
			input:       "//server/share",
			shouldError: true,
			errorMsg:    "dangerous sequence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.ValidateAndSanitize(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
			}
		})
	}
}

func TestValidateAndSanitizePermissive(t *testing.T) {
	v := NewPermissiveFilePathValidator()
	tempDir := os.TempDir()

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "relative path with permissive validator",
			input:       "test.txt", // Simple relative path without ./
			shouldError: false,
		},
		{
			name:        "path in temp dir",
			input:       filepath.Join(tempDir, "test.txt"),
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := v.ValidateAndSanitize(tt.input)
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input %q", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for permissive validation of %q: %v", tt.input, err)
			}
			if !tt.shouldError && result == "" {
				t.Error("Expected non-empty result for valid input")
			}
		})
	}
}

func TestHomeExpansion(t *testing.T) {
	v := NewFilePathValidator()

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "valid tilde expansion",
			input:       "~/.fwrd/test.db",
			shouldError: false,
		},
		{
			name:        "invalid tilde usage",
			input:       "~test",
			shouldError: true,
		},
		{
			name:        "tilde in middle",
			input:       filepath.Join(os.TempDir(), "~test"), // Use temp dir for allowed base dirs
			shouldError: false,                                // This should be fine
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.ValidateAndSanitize(tt.input)
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input %q", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
			}
		})
	}
}

func TestValidateCharacters(t *testing.T) {
	v := NewFilePathValidator()

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "clean path",
			input:       "/tmp/test.txt",
			shouldError: false,
		},
		{
			name:        "path with tab (allowed)",
			input:       "/tmp/test\ttab.txt",
			shouldError: false,
		},
		{
			name:        "null byte",
			input:       "/tmp/test\x00.txt",
			shouldError: true,
		},
		{
			name:        "control character",
			input:       "/tmp/test\x01.txt",
			shouldError: true,
		},
		{
			name:        "directory traversal unix",
			input:       "../../../etc/passwd",
			shouldError: true,
		},
		{
			name:        "directory traversal windows",
			input:       "..\\..\\windows\\system32",
			shouldError: true,
		},
		{
			name:        "current directory reference",
			input:       "./file.txt",
			shouldError: true, // Dangerous in secure mode
		},
		{
			name:        "UNC path",
			input:       "\\\\server\\share",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.validateCharacters(tt.input)
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input %q", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	homeDir, _ := os.UserHomeDir()

	tests := []struct {
		name        string
		validator   *FilePathValidator
		input       string
		expected    string
		shouldError bool
	}{
		{
			name:      "tilde expansion",
			validator: NewFilePathValidator(),
			input:     "~/.fwrd/test.db",
			expected:  filepath.Join(homeDir, ".fwrd", "test.db"),
		},
		{
			name:        "tilde expansion disabled",
			validator:   &FilePathValidator{AllowHomeExpansion: false},
			input:       "~/.fwrd/test.db",
			shouldError: true,
		},
		{
			name:      "relative to absolute conversion",
			validator: &FilePathValidator{AllowRelativePaths: false},
			input:     "test.txt",
		},
		{
			name:        "clean path with traversal",
			validator:   NewFilePathValidator(),
			input:       "/tmp//test/../file.txt",
			shouldError: true, // Contains .. which is blocked
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.validator.normalizePath(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if tt.expected != "" && result != tt.expected {
					t.Errorf("Expected %q, got %q", tt.expected, result)
				}
			}
		})
	}
}

func TestValidateTraversal(t *testing.T) {
	v := NewFilePathValidator()

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "clean absolute path",
			input:       "/tmp/test/file.txt",
			shouldError: false,
		},
		{
			name:        "parent directory traversal",
			input:       "/tmp/../etc/passwd",
			shouldError: true,
		},
		{
			name:        "current directory in secure mode",
			input:       "/tmp/./file.txt",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.validateTraversal(tt.input)
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input %q", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
			}
		})
	}
}

func TestValidateBaseDirs(t *testing.T) {
	tempDir := os.TempDir()
	v := &FilePathValidator{
		AllowedBaseDirs: []string{tempDir},
	}

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "path within allowed directory",
			input:       filepath.Join(tempDir, "test.txt"),
			shouldError: false,
		},
		{
			name:        "path outside allowed directory",
			input:       "/etc/passwd",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.validateBaseDirs(tt.input)
			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input %q", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
			}
		})
	}
}

func TestValidateDirectory(t *testing.T) {
	v := NewPermissiveFilePathValidator()
	tempDir := os.TempDir()

	tests := []struct {
		name             string
		path             string
		createIfNotExist bool
		shouldError      bool
		shouldCreateDir  bool
	}{
		{
			name:             "existing directory",
			path:             tempDir,
			createIfNotExist: false,
			shouldError:      false,
		},
		{
			name:             "non-existent directory, don't create",
			path:             filepath.Join(tempDir, "nonexistent"),
			createIfNotExist: false,
			shouldError:      false, // Should not error, just return the path
		},
		{
			name:             "non-existent directory, create",
			path:             filepath.Join(tempDir, "test_create_dir"),
			createIfNotExist: true,
			shouldError:      false,
			shouldCreateDir:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up before test
			if tt.shouldCreateDir {
				os.RemoveAll(tt.path)
			}

			result, err := v.ValidateDirectory(tt.path, tt.createIfNotExist)

			if tt.shouldError && err == nil {
				t.Errorf("Expected error for path %q", tt.path)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for path %q: %v", tt.path, err)
			}

			if !tt.shouldError {
				if result == "" {
					t.Error("Expected non-empty result path")
				}

				if tt.shouldCreateDir {
					if _, err := os.Stat(tt.path); os.IsNotExist(err) {
						t.Errorf("Directory was not created: %s", tt.path)
					}
					// Clean up after test
					os.RemoveAll(tt.path)
				}
			}
		})
	}
}

func TestValidateFile(t *testing.T) {
	v := NewPermissiveFilePathValidator()
	tempDir := os.TempDir()

	// Create a test file
	testFile := filepath.Join(tempDir, "test_validate_file.txt")
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()
	defer os.Remove(testFile)

	// Create a test directory that we'll try to validate as a file
	testDir := filepath.Join(tempDir, "test_validate_dir")
	os.Mkdir(testDir, 0o755)
	defer os.RemoveAll(testDir)

	tests := []struct {
		name        string
		path        string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "valid file",
			path:        testFile,
			shouldError: false,
		},
		{
			name:        "non-existent file",
			path:        filepath.Join(tempDir, "nonexistent.txt"),
			shouldError: false, // Should not error for non-existent files
		},
		{
			name:        "directory instead of file",
			path:        testDir,
			shouldError: true,
			errorMsg:    "path is a directory, not a file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := v.ValidateFile(tt.path)

			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for path %q", tt.path)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %q: %v", tt.path, err)
				}
				if result == "" {
					t.Error("Expected non-empty result path")
				}
			}
		})
	}
}

func TestIsPathSafe(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "safe path",
			path:     "/tmp/test.txt",
			expected: true,
		},
		{
			name:     "null byte injection",
			path:     "/tmp/test\x00.txt",
			expected: false,
		},
		{
			name:     "directory traversal unix",
			path:     "../../../etc/passwd",
			expected: false,
		},
		{
			name:     "directory traversal windows",
			path:     "..\\..\\windows",
			expected: false,
		},
		{
			name:     "path too long",
			path:     strings.Repeat("a", 5000),
			expected: false,
		},
		{
			name:     "normal long path within limit",
			path:     strings.Repeat("a", 4000),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPathSafe(tt.path)
			if result != tt.expected {
				t.Errorf("IsPathSafe(%q) = %v, expected %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestValidatorEdgeCases(t *testing.T) {
	v := &FilePathValidator{
		AllowedBaseDirs:    []string{"/invalid/directory/that/does/not/exist"},
		AllowHomeExpansion: true,
		AllowRelativePaths: false,
		MaxPathLength:      4096,
	}

	// Test with invalid base directories
	tempDir := os.TempDir()
	_, err := v.ValidateAndSanitize(filepath.Join(tempDir, "test.txt"))
	if err == nil {
		t.Error("Expected error when path is not within allowed base directories")
	}

	// Test empty allowed base directories (should allow all paths)
	v.AllowedBaseDirs = []string{}
	_, err = v.ValidateAndSanitize(filepath.Join(tempDir, "test.txt"))
	if err != nil {
		t.Errorf("Unexpected error with empty allowed base directories: %v", err)
	}
}
