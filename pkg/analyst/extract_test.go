package analyst

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// A docx is a zip with word/document.xml; extractText should pull the paragraph text out (review #2).
func TestExtractDocx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body>` +
		`<w:p><w:r><w:t>Hello from</w:t><w:t> the report</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Second line</w:t></w:r></w:p></w:body></w:document>`))
	_ = zw.Close()

	text, ok := extractText(buf.Bytes())
	if !ok {
		t.Fatal("expected docx to be extractable")
	}
	if !strings.Contains(text, "Hello from the report") || !strings.Contains(text, "Second line") {
		t.Fatalf("extracted text wrong: %q", text)
	}
}

// A plain binary blob has no extractor → not extractable.
func TestExtractUnknownBinary(t *testing.T) {
	if _, ok := extractText([]byte{0x00, 0x01, 0x02, 0xff}); ok {
		t.Fatal("random bytes should not be extractable")
	}
}
