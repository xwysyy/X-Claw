package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pdf "github.com/ledongthuc/pdf"
)

type DocumentTextTool struct {
	workspace string
	restrict  bool
}

func NewDocumentTextTool(workspace string, restrict bool) *DocumentTextTool {
	return &DocumentTextTool{workspace: workspace, restrict: restrict}
}

func (t *DocumentTextTool) Name() string {
	return "document_text"
}

func (t *DocumentTextTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *DocumentTextTool) Description() string {
	return "Extract readable plain text from a PDF or DOCX document stored in the workspace. Use this for user-uploaded documents."
}

func (t *DocumentTextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the document file (prefer workspace-relative paths like uploads/...)",
			},
			"max_chars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to return (default: 12000)",
				"minimum":     200.0,
			},
		},
		"required": []string{"path"},
	}
}

func (t *DocumentTextTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	rawPath, ok := args["path"].(string)
	if !ok || strings.TrimSpace(rawPath) == "" {
		return ErrorResult("path is required")
	}

	maxChars, err := parseOptionalIntArg(args, "max_chars", 12000, 200, 200000)
	if err != nil {
		return ErrorResult(err.Error())
	}

	resolvedPath, err := validatePath(rawPath, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	ext := strings.ToLower(filepath.Ext(resolvedPath))

	info, _ := os.Stat(resolvedPath)
	sizeBytes := int64(0)
	if info != nil {
		sizeBytes = info.Size()
	}

	var extracted string
	var truncated bool

	switch ext {
	case ".pdf":
		extracted, truncated, err = extractPDFPlainText(ctx, resolvedPath, maxChars)
	case ".docx":
		extracted, truncated, err = extractDOCXPlainText(ctx, resolvedPath, maxChars)
	default:
		return ErrorResult(fmt.Sprintf("unsupported document type %q (supported: .pdf, .docx)", ext))
	}
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	extracted = strings.TrimSpace(extracted)
	if extracted == "" {
		// Common for scanned PDFs or documents that contain only images.
		return SilentResult(fmt.Sprintf("[document_text] path=%s type=%s size=%dB extracted_chars=0 (no extractable text found)", rawPath, ext, sizeBytes))
	}

	header := fmt.Sprintf(
		"[document_text] path=%s type=%s size=%dB extracted_chars=%d truncated=%v",
		rawPath,
		ext,
		sizeBytes,
		len([]rune(extracted)),
		truncated,
	)
	return SilentResult(header + "\n\n" + extracted)
}

func extractPDFPlainText(ctx context.Context, path string, maxChars int) (string, bool, error) {
	const defaultTimeout = 25 * time.Second

	// Avoid hanging on malformed PDFs.
	runCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	type result struct {
		text      string
		truncated bool
		err       error
	}

	ch := make(chan result, 1)
	go func() {
		f, r, err := pdf.Open(path)
		if err != nil {
			ch <- result{err: fmt.Errorf("pdf open failed: %w", err)}
			return
		}
		defer f.Close()

		plain, err := r.GetPlainText()
		if err != nil {
			ch <- result{err: fmt.Errorf("pdf text extraction failed: %w", err)}
			return
		}

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, plain); err != nil {
			ch <- result{err: fmt.Errorf("pdf read extracted text failed: %w", err)}
			return
		}

		text, truncated := truncateRunes(strings.TrimSpace(buf.String()), maxChars)
		ch <- result{text: text, truncated: truncated}
	}()

	select {
	case <-runCtx.Done():
		return "", false, fmt.Errorf("pdf text extraction timed out: %w", runCtx.Err())
	case res := <-ch:
		return res.text, res.truncated, res.err
	}
}

func extractDOCXPlainText(ctx context.Context, path string, maxChars int) (string, bool, error) {
	_ = ctx

	const maxXMLBytes = 12 * 1024 * 1024 // 12MB cap for document.xml

	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false, fmt.Errorf("docx open failed: %w", err)
	}
	defer zr.Close()

	var doc *zip.File
	for _, f := range zr.File {
		if f != nil && f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", false, fmt.Errorf("docx missing word/document.xml")
	}

	rc, err := doc.Open()
	if err != nil {
		return "", false, fmt.Errorf("docx open document.xml failed: %w", err)
	}
	defer rc.Close()

	dec := xml.NewDecoder(io.LimitReader(rc, maxXMLBytes))
	var sb strings.Builder
	runeCount := 0
	truncated := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("docx parse failed: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "t":
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return "", false, fmt.Errorf("docx parse text failed: %w", err)
				}
				if text != "" {
					sb.WriteString(text)
					runeCount += len([]rune(text))
				}
			case "tab":
				sb.WriteString("\t")
				runeCount++
			case "br", "cr":
				sb.WriteString("\n")
				runeCount++
			}
		case xml.EndElement:
			if strings.ToLower(t.Name.Local) == "p" {
				sb.WriteString("\n")
				runeCount++
			}
		}

		if maxChars > 0 && runeCount >= maxChars {
			truncated = true
			break
		}
	}

	text, more := truncateRunes(strings.TrimSpace(sb.String()), maxChars)
	if more {
		truncated = true
	}
	return text, truncated, nil
}

func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s, false
	}
	return string(rs[:max]), true
}
