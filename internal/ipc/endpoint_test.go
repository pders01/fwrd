package ipc

import "testing"

func TestNormalizeCanonicalPathWindowsCaseInsensitive(t *testing.T) {
	upper := normalizeCanonicalPath(`C:\Users\Example\.FWRD\FWRD.DB`, true)
	lower := normalizeCanonicalPath(`c:\users\example\.fwrd\fwrd.db`, true)
	if upper != lower {
		t.Fatalf("Windows aliases produced different identities: %q != %q", upper, lower)
	}
	if NewEndpoint(upper).DatabaseID != NewEndpoint(lower).DatabaseID {
		t.Fatal("Windows case aliases produced different database IDs")
	}
}
