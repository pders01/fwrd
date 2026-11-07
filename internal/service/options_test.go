package service

import (
	"path/filepath"
	"testing"
)

func TestPrepareOptionsResolvesPathsWithoutMutatingCaller(t *testing.T) {
	original := &Options{BinPath: "bin/fwrd", Config: "config/settings.toml", DB: "data/news.db", TLSCert: "tls/cert.pem", TLSKey: "tls/key.pem"}
	prepared, err := prepareOptions(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{prepared.BinPath, prepared.Config, prepared.DB, prepared.TLSCert, prepared.TLSKey} {
		if !filepath.IsAbs(path) {
			t.Fatalf("service path remains relative: %q", path)
		}
	}
	if original.BinPath != "bin/fwrd" || original.DB != "data/news.db" {
		t.Fatal("caller options changed")
	}
	prepared, err = prepareOptions(&Options{DB: ":memory:"})
	if err != nil || prepared.DB != ":memory:" {
		t.Fatal("memory sentinel changed")
	}
}

func TestPrepareOptionsRejectsControlCharacters(t *testing.T) {
	if _, err := prepareOptions(&Options{Config: "settings\nInjected.toml"}); err == nil {
		t.Fatal("unsafe service path accepted")
	}
}
