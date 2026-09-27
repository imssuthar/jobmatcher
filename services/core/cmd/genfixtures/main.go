// Command genfixtures renders the text fixtures into the PDF and DOCX files
// used by acceptance tests, so the test inputs are reproducible from source.
//
//	go run ./cmd/genfixtures -dir ../../testdata/fixtures
package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

func main() {
	dir := flag.String("dir", "testdata/fixtures", "fixtures directory")
	flag.Parse()

	if err := writePDF(filepath.Join(*dir, "alex_rivera.txt"), filepath.Join(*dir, "alex_rivera.pdf")); err != nil {
		log.Fatal(err)
	}
	if err := writeDOCX(filepath.Join(*dir, "priya_nair.txt"), filepath.Join(*dir, "priya_nair.docx")); err != nil {
		log.Fatal(err)
	}
	// A file that looks like a PDF but is not one, to test failure handling.
	if err := os.WriteFile(filepath.Join(*dir, "corrupt.pdf"), []byte("%PDF-1.4\nthis is not really a pdf\n%%EOF\n"), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("fixtures written to", *dir)
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

func writePDF(src, dst string) error {
	lines, err := readLines(src)
	if err != nil {
		return err
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCreationDate(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) // deterministic output
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()
	for i, line := range lines {
		switch {
		case i == 0:
			pdf.SetFont("Helvetica", "B", 18)
		case line != "" && strings.ToUpper(line) == line:
			pdf.SetFont("Helvetica", "B", 12)
		default:
			pdf.SetFont("Helvetica", "", 10.5)
		}
		pdf.MultiCell(0, 5.5, line, "", "L", false)
	}
	return pdf.OutputFileAndClose(dst)
}

func writeDOCX(src, dst string) error {
	lines, err := readLines(src)
	if err != nil {
		return err
	}
	var body strings.Builder
	for _, line := range lines {
		var esc bytes.Buffer
		_ = xml.EscapeText(&esc, []byte(line))
		fmt.Fprintf(&body, `<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, esc.String())
	}
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}
