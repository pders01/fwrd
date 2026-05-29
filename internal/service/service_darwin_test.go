//go:build darwin

package service

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestPlistContent_WellFormedAndComplete(t *testing.T) {
	// A path with an XML metacharacter must not break the document.
	got, err := plistContent(&Options{
		BinPath:  "/Applications/fwrd & co/fwrd",
		Addr:     "0.0.0.0:8080",
		MDNS:     true,
		MDNSName: "fwrd",
	}, "/Users/test/.fwrd")
	if err != nil {
		t.Fatalf("plistContent: %v", err)
	}

	// Parses as XML → well-formed (escaping correct).
	if err := xml.Unmarshal(got, new(any)); err != nil {
		t.Fatalf("plist is not well-formed XML: %v\n%s", err, got)
	}

	s := string(got)
	for _, want := range []string{
		"<key>Label</key><string>com.fwrd.serve</string>",
		"<string>serve</string>",
		"<string>--addr</string>",
		"<string>0.0.0.0:8080</string>",
		"<string>--mdns</string>",
		"&amp;", // the ampersand in the bin path, escaped
		"serve.out.log",
		"<key>RunAtLoad</key><true/>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q in:\n%s", want, s)
		}
	}
}
