package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// FormatDOCX is a Word document, generated directly from Data (no browser needed).
const FormatDOCX Format = "docx"

// DOCX renders report Data to a minimal, valid .docx (WordprocessingML). Headings use bold/sized
// runs so no styles.xml is required — it opens in Word/LibreOffice/Google Docs. The layout follows
// the technical report; title carries the chosen template's title.
func DOCX(title string, d Data) ([]byte, error) {
	var body strings.Builder
	heading(&body, 1, d.Project.Name+" — "+title)
	para(&body, "Generated "+d.GeneratedAt.Format("2006-01-02 15:04 MST"))

	heading(&body, 2, "Summary")
	para(&body, fmt.Sprintf("%d finding(s) with supporting evidence across %d application(s), %d task(s) run.",
		d.Summary.Total, d.Summary.Applications, d.Summary.TasksRun))
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if n := d.Summary.BySeverity[sev]; n > 0 {
			para(&body, fmt.Sprintf("  %s: %d", strings.ToUpper(sev), n))
		}
	}

	if d.Methodology.Summary.Total > 0 {
		heading(&body, 2, "Methodology coverage")
		para(&body, fmt.Sprintf("%d%% covered — %d of %d items (%d n/a).",
			d.Methodology.Summary.CoveredPct, d.Methodology.Summary.Covered,
			d.Methodology.Summary.Total, d.Methodology.Summary.NotApplicable))
	}

	heading(&body, 2, "Findings")
	if len(d.Findings) == 0 {
		para(&body, "No reportable findings.")
	}
	for _, f := range d.Findings {
		heading(&body, 3, "["+strings.ToUpper(f.Severity)+"] "+f.Title)
		meta := f.AppName + " · " + f.Status
		if f.CWE != "" {
			meta += " · " + f.CWE
		}
		para(&body, meta)
		if f.Description != "" {
			para(&body, f.Description)
		}
		for _, e := range f.Evidence {
			line := "• " + e.Title
			if e.Location != "" {
				line += " — " + e.Location
			}
			para(&body, line)
		}
	}

	document := xmlHeader + `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() +
		`<w:sectPr/></w:body></w:document>`

	return zipDocx(document)
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func heading(b *strings.Builder, level int, text string) {
	size := map[int]string{1: "36", 2: "28", 3: "24"}[level]
	fmt.Fprintf(b, `<w:p><w:pPr><w:spacing w:before="200" w:after="80"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="%s"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		size, xmlEscape(text))
}

func para(b *strings.Builder, text string) {
	fmt.Fprintf(b, `<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlEscape(text))
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func zipDocx(documentXML string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"_rels/.rels": xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
		"word/document.xml": documentXML,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
