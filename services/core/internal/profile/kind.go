package profile

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ErrUnsupportedType is returned for files that are not PDF, DOCX or text.
var ErrUnsupportedType = errors.New("unsupported file type: upload a PDF, DOCX, TXT or MD resume")

// DetectType decides the content type from magic bytes and the extension.
// It returns the normalized content type and file extension.
func DetectType(filename string, data []byte) (contentType, ext string, err error) {
	ext = strings.ToLower(filepath.Ext(filename))
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf", ".pdf", nil
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) && ext == ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx", nil
	case (ext == ".txt" || ext == ".md") && utf8.Valid(data) && !bytes.ContainsRune(data, 0):
		return "text/plain; charset=utf-8", ext, nil
	}
	return "", "", ErrUnsupportedType
}
