package agent

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestDocumentTextTool_DOCX_Generated(t *testing.T) {
	workspace := t.TempDir()

	docxRel := filepath.Join("uploads", "test.docx")
	docxAbs := filepath.Join(workspace, docxRel)
	if err := os.MkdirAll(filepath.Dir(docxAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	f, err := os.Create(docxAbs)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("zip create failed: %v", err)
	}

	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello</w:t></w:r></w:p>
    <w:p><w:r><w:t>World</w:t></w:r></w:p>
  </w:body>
</w:document>`))
	if err != nil {
		t.Fatalf("zip write failed: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file close failed: %v", err)
	}

	tool := tools.NewDocumentTextTool(workspace, true)
	res := tool.Execute(context.Background(), map[string]any{
		"path":      docxRel,
		"max_chars": 2000,
	})
	if res == nil {
		t.Fatalf("expected result, got nil")
	}
	if res.IsError {
		t.Fatalf("expected no error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Hello") || !strings.Contains(res.ForLLM, "World") {
		t.Fatalf("expected extracted text in result, got: %s", res.ForLLM)
	}
}

func TestDocumentTextTool_DOCX_Fixture(t *testing.T) {
	workspace := t.TempDir()

	srcPath := filepath.Join("testdata", "documents", "2.docx")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("ReadFile fixture failed: %v", err)
	}

	docxRel := filepath.Join("uploads", "fixture.docx")
	docxAbs := filepath.Join(workspace, docxRel)
	if err := os.MkdirAll(filepath.Dir(docxAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(docxAbs, data, 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	tool := tools.NewDocumentTextTool(workspace, true)
	res := tool.Execute(context.Background(), map[string]any{
		"path":      docxRel,
		"max_chars": 12000,
	})
	if res == nil {
		t.Fatalf("expected result, got nil")
	}
	if res.IsError {
		t.Fatalf("expected no error, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "extracted_chars=0") {
		t.Fatalf("expected non-empty extracted text, got: %s", res.ForLLM)
	}
}

func TestDocumentTextTool_PDF_Fixture(t *testing.T) {
	workspace := t.TempDir()

	srcPath := filepath.Join("testdata", "documents", "2.pdf")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("ReadFile fixture failed: %v", err)
	}

	pdfRel := filepath.Join("uploads", "fixture.pdf")
	pdfAbs := filepath.Join(workspace, pdfRel)
	if err := os.MkdirAll(filepath.Dir(pdfAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(pdfAbs, data, 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	tool := tools.NewDocumentTextTool(workspace, true)
	res := tool.Execute(context.Background(), map[string]any{
		"path":      pdfRel,
		"max_chars": 12000,
	})
	if res == nil {
		t.Fatalf("expected result, got nil")
	}
	if res.IsError {
		t.Fatalf("expected no error, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "extracted_chars=0") {
		t.Fatalf("expected non-empty extracted text, got: %s", res.ForLLM)
	}
}

func TestDocumentTextTool_UnsupportedDoc_Fixture(t *testing.T) {
	workspace := t.TempDir()

	srcPath := filepath.Join("testdata", "documents", "1.doc")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("ReadFile fixture failed: %v", err)
	}

	docRel := filepath.Join("uploads", "fixture.doc")
	docAbs := filepath.Join(workspace, docRel)
	if err := os.MkdirAll(filepath.Dir(docAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(docAbs, data, 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	tool := tools.NewDocumentTextTool(workspace, true)
	res := tool.Execute(context.Background(), map[string]any{
		"path":      docRel,
		"max_chars": 2000,
	})
	if res == nil {
		t.Fatalf("expected result, got nil")
	}
	if !res.IsError {
		t.Fatalf("expected error for unsupported .doc, got: %s", res.ForLLM)
	}
	if !strings.Contains(strings.ToLower(res.ForLLM), "unsupported") {
		t.Fatalf("expected unsupported error message, got: %s", res.ForLLM)
	}
}
