package analyst

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extractText pulls readable text out of a binary document so the analyst can actually read ingested PDFs and
// Office files (ADR-0020, review #2). It handles PDF and OOXML (docx/pptx) — both parsed on-host so no
// document content leaves the machine. Returns (text, true) on success; (_, false) for formats it can't read,
// so the caller falls back to a metadata note.
func extractText(data []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF")):
		if t, err := extractPDF(data); err == nil && strings.TrimSpace(t) != "" {
			return t, true
		}
	case bytes.HasPrefix(data, []byte("PK\x03\x04")): // zip container → OOXML
		if t, err := extractOOXML(data); err == nil && strings.TrimSpace(t) != "" {
			return t, true
		}
	}
	return "", false
}

func extractPDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	tr, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if _, err := io.Copy(&b, tr); err != nil {
		return "", err
	}
	return b.String(), nil
}

// extractOOXML reads text from the XML parts of a Word (word/document.xml) or PowerPoint (ppt/slides/*.xml)
// file. It walks the XML and emits character data, breaking a line at each paragraph end (`w:p` / `a:p`).
func extractOOXML(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, f := range zr.File {
		name := f.Name
		if name != "word/document.xml" && !(strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		out.WriteString(xmlPlainText(raw))
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// xmlPlainText concatenates the character data of an XML document, inserting a newline at each paragraph end.
func xmlPlainText(raw []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local == "p" {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
