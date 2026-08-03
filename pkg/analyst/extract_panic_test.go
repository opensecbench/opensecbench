package analyst

import (
	"bytes"
	"strings"
	"testing"
)

// safeExtract must convert a parser panic into an error rather than let it crash the process.
func TestSafeExtractRecoversPanic(t *testing.T) {
	txt, err := safeExtract(func() (string, error) { panic("boom") })
	if err == nil {
		t.Fatal("expected a panic to be returned as an error")
	}
	if txt != "" {
		t.Fatalf("expected empty text on panic, got %q", txt)
	}
}

// A file that claims to be a PDF but is garbage must degrade to (_, false), never panic.
func TestExtractTextMalformedDocsDoNotPanic(t *testing.T) {
	inputs := [][]byte{
		[]byte("%PDF-1.4\nxref\ntrailer garbage"),
		append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0xff, 0x00, 0x01, 0x80}, 40)...),
		[]byte("%PDF"),
		append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0x00}, 32)...), // bogus zip/OOXML
	}
	for _, in := range inputs {
		txt, ok := extractText(in) // must not panic
		if ok && strings.TrimSpace(txt) == "" {
			t.Errorf("reported success with empty text for %q", in)
		}
	}
}
