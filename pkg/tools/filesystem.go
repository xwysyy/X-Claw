package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pdf "github.com/ledongthuc/pdf"

	"github.com/xwysyy/X-Claw/pkg/fileutil"
)

// validatePath ensures the given path is within the workspace if restrict is true.
func validatePath(path, workspace string, restrict bool) (string, error) {
	if workspace == "" {
		return path, fmt.Errorf("workspace is not defined")
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath, err = filepath.Abs(filepath.Join(absWorkspace, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve file path: %w", err)
		}
	}

	if restrict {
		if !isWithinWorkspace(absPath, absWorkspace) {
			return "", fmt.Errorf("access denied: path is outside the workspace")
		}

		var resolved string
		workspaceReal := absWorkspace
		if resolved, err = filepath.EvalSymlinks(absWorkspace); err == nil {
			workspaceReal = resolved
		}

		if resolved, err = filepath.EvalSymlinks(absPath); err == nil {
			if !isWithinWorkspace(resolved, workspaceReal) {
				return "", fmt.Errorf("access denied: symlink resolves outside workspace")
			}
		} else if os.IsNotExist(err) {
			var parentResolved string
			if parentResolved, err = resolveExistingAncestor(filepath.Dir(absPath)); err == nil {
				if !isWithinWorkspace(parentResolved, workspaceReal) {
					return "", fmt.Errorf("access denied: symlink resolves outside workspace")
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to resolve path: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}

	return absPath, nil
}

func resolveExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && filepath.IsLocal(rel)
}

type ReadFileTool struct {
	fs           fileSystem
	maxReadBytes int
}

func NewReadFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ReadFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &ReadFileTool{
		fs:           buildFs(workspace, restrict, patterns),
		maxReadBytes: 30_000,
	}
}

// SetMaxReadBytes overrides the default read cap for the read_file tool.
// It is a safety guard to prevent OOM when reading unexpectedly large files.
func (t *ReadFileTool) SetMaxReadBytes(maxBytes int) {
	if t == nil {
		return
	}
	if maxBytes <= 0 {
		return
	}
	// Hard cap to avoid foot-guns.
	if maxBytes > 5*1024*1024 {
		maxBytes = 5 * 1024 * 1024
	}
	t.maxReadBytes = maxBytes
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a file and return its text. " +
		"Input: path (string, required). " +
		"Output: the file's full text content. " +
		"Use this instead of 'exec' with cat/head/tail for reading files."
}

func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional byte offset to start reading from (default 0).",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"description": "Optional maximum bytes to read (default is a safety cap).",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	if t == nil || t.fs == nil {
		return ErrorResult("filesystem is not configured")
	}

	maxBytes := t.maxReadBytes
	if maxBytes <= 0 {
		maxBytes = 30_000
	}
	// Allow override per-call, but keep reasonable bounds.
	parsedMax, err := parseOptionalIntArg(args, "max_bytes", maxBytes, 200, 5*1024*1024)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	maxBytes = parsedMax

	offset, err := parseOptionalIntArg(args, "offset", 0, 0, 1_000_000_000)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	fi, statErr := t.fs.Stat(path)
	if statErr != nil || fi == nil {
		// Fallback: read the first maxBytes as best-effort.
		buf, readErr := t.fs.ReadFileRange(path, int64(offset), int64(maxBytes))
		if readErr != nil {
			return ErrorResult(readErr.Error()).WithError(readErr)
		}
		return NewToolResult(string(buf))
	}

	size := fi.Size()
	if size < 0 {
		size = 0
	}
	off := int64(offset)
	if off > size {
		return ErrorResult(fmt.Sprintf("offset out of range: offset=%d, file_size=%d", off, size))
	}

	remaining := size - off
	if remaining <= int64(maxBytes) {
		buf, readErr := t.fs.ReadFileRange(path, off, remaining)
		if readErr != nil {
			return ErrorResult(readErr.Error()).WithError(readErr)
		}
		return NewToolResult(string(buf))
	}

	// Truncated: if offset is non-zero, return a straight slice from offset.
	if off > 0 {
		buf, readErr := t.fs.ReadFileRange(path, off, int64(maxBytes))
		if readErr != nil {
			return ErrorResult(readErr.Error()).WithError(readErr)
		}
		note := fmt.Sprintf(
			"\n...\n[read_file truncated: file_size=%d bytes, offset=%d, max_bytes=%d]\n...\n",
			size,
			off,
			maxBytes,
		)
		return NewToolResult(string(buf) + note)
	}

	// Truncated from the beginning: return head + tail (keeps useful context).
	headBytes := maxBytes * 7 / 10
	tailBytes := maxBytes * 2 / 10
	if headBytes <= 0 {
		headBytes = maxBytes
		tailBytes = 0
	}
	if headBytes+tailBytes > maxBytes {
		tailBytes = maxBytes - headBytes
		if tailBytes < 0 {
			tailBytes = 0
		}
	}

	head, readErr := t.fs.ReadFileRange(path, 0, int64(headBytes))
	if readErr != nil {
		return ErrorResult(readErr.Error()).WithError(readErr)
	}

	tail := []byte(nil)
	if tailBytes > 0 && size > int64(tailBytes) {
		tail, readErr = t.fs.ReadFileRange(path, size-int64(tailBytes), int64(tailBytes))
		if readErr != nil {
			return ErrorResult(readErr.Error()).WithError(readErr)
		}
	}

	marker := fmt.Sprintf(
		"\n...\n[read_file truncated: file_size=%d bytes, max_bytes=%d]\n...\n",
		size,
		maxBytes,
	)
	return NewToolResult(string(head) + marker + string(tail))
}

type WriteFileTool struct {
	fs fileSystem
}

func NewWriteFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *WriteFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &WriteFileTool{fs: buildFs(workspace, restrict, patterns)}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Create or overwrite a file with the given content. " +
		"Input: path (string, required), content (string, required). " +
		"Output: confirmation of the write. " +
		"Warning: this overwrites the entire file. To modify part of a file, use 'edit_file' instead."
}

func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if err := t.fs.WriteFile(path, []byte(content)); err != nil {
		return ErrorResult(err.Error())
	}

	return SilentResult(fmt.Sprintf("File written: %s", path))
}

type ListDirTool struct {
	fs fileSystem
}

func NewListDirTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ListDirTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &ListDirTool{fs: buildFs(workspace, restrict, patterns)}
}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a given path. " +
		"Input: path (string, optional — defaults to workspace root). " +
		"Output: list of entries with name, type (file/dir), size, and modification time."
}

func (t *ListDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to list",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := t.fs.ReadDir(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}
	return formatDirEntries(entries)
}

func formatDirEntries(entries []os.DirEntry) *ToolResult {
	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString("DIR:  " + entry.Name() + "\n")
		} else {
			result.WriteString("FILE: " + entry.Name() + "\n")
		}
	}
	return NewToolResult(result.String())
}

// fileSystem abstracts reading, writing, and listing files, allowing both
// unrestricted (host filesystem) and sandbox (os.Root) implementations to share the same polymorphic interface.
type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	ReadFileRange(path string, offset, length int64) ([]byte, error)
	Stat(path string) (os.FileInfo, error)
	WriteFile(path string, data []byte) error
	ReadDir(path string) ([]os.DirEntry, error)
}

// hostFs is an unrestricted fileReadWriter that operates directly on the host filesystem.
type hostFs struct{}

func (h *hostFs) ReadFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to read file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func (h *hostFs) ReadFileRange(path string, offset, length int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to read file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	defer f.Close()

	if offset < 0 {
		return nil, fmt.Errorf("failed to read file: invalid offset %d", offset)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to read file: seek failed: %w", err)
		}
	}
	if length <= 0 {
		return []byte{}, nil
	}

	limited := io.LimitReader(f, length)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func (h *hostFs) Stat(path string) (os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to stat file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	return fi, nil
}

func (h *hostFs) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func (h *hostFs) WriteFile(path string, data []byte) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

// sandboxFs is a sandboxed fileSystem that operates within a strictly defined workspace using os.Root.
type sandboxFs struct {
	workspace string
}

func (r *sandboxFs) execute(path string, fn func(root *os.Root, relPath string) error) error {
	if r.workspace == "" {
		return fmt.Errorf("workspace is not defined")
	}

	root, err := os.OpenRoot(r.workspace)
	if err != nil {
		return fmt.Errorf("failed to open workspace: %w", err)
	}
	defer root.Close()

	relPath, err := getSafeRelPath(r.workspace, path)
	if err != nil {
		return err
	}

	return fn(root, relPath)
}

func (r *sandboxFs) ReadFile(path string) ([]byte, error) {
	var content []byte
	err := r.execute(path, func(root *os.Root, relPath string) error {
		fileContent, err := root.ReadFile(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to read file: file not found: %w", err)
			}
			// os.Root returns "escapes from parent" for paths outside the root
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to read file: access denied: %w", err)
			}
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = fileContent
		return nil
	})
	return content, err
}

func (r *sandboxFs) ReadFileRange(path string, offset, length int64) ([]byte, error) {
	var content []byte
	err := r.execute(path, func(root *os.Root, relPath string) error {
		f, err := root.Open(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to read file: file not found: %w", err)
			}
			// os.Root returns "escapes from parent" for paths outside the root
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to read file: access denied: %w", err)
			}
			return fmt.Errorf("failed to read file: %w", err)
		}
		defer f.Close()

		if offset < 0 {
			return fmt.Errorf("failed to read file: invalid offset %d", offset)
		}
		if offset > 0 {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return fmt.Errorf("failed to read file: seek failed: %w", err)
			}
		}
		if length <= 0 {
			content = []byte{}
			return nil
		}

		limited := io.LimitReader(f, length)
		fileContent, err := io.ReadAll(limited)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = fileContent
		return nil
	})
	return content, err
}

func (r *sandboxFs) Stat(path string) (os.FileInfo, error) {
	var fi os.FileInfo
	err := r.execute(path, func(root *os.Root, relPath string) error {
		info, err := root.Stat(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to stat file: file not found: %w", err)
			}
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to stat file: access denied: %w", err)
			}
			return fmt.Errorf("failed to stat file: %w", err)
		}
		fi = info
		return nil
	})
	return fi, err
}

func (r *sandboxFs) WriteFile(path string, data []byte) error {
	return r.execute(path, func(root *os.Root, relPath string) error {
		dir := filepath.Dir(relPath)
		if dir != "." && dir != "/" {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create parent directories: %w", err)
			}
		}

		// Use atomic write pattern with explicit sync for flash storage reliability.
		// Using 0o600 (owner read/write only) for secure default permissions.
		tmpRelPath := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())

		tmpFile, err := root.OpenFile(tmpRelPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to open temp file: %w", err)
		}

		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}

		// CRITICAL: Force sync to storage medium before rename.
		// This ensures data is physically written to disk, not just cached.
		if err := tmpFile.Sync(); err != nil {
			tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to sync temp file: %w", err)
		}

		if err := tmpFile.Close(); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to close temp file: %w", err)
		}

		if err := root.Rename(tmpRelPath, relPath); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to rename temp file over target: %w", err)
		}

		// Sync directory to ensure rename is durable
		if dirFile, err := root.Open("."); err == nil {
			_ = dirFile.Sync()
			dirFile.Close()
		}

		return nil
	})
}

func (r *sandboxFs) ReadDir(path string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	err := r.execute(path, func(root *os.Root, relPath string) error {
		dirEntries, err := fs.ReadDir(root.FS(), relPath)
		if err != nil {
			return err
		}
		entries = dirEntries
		return nil
	})
	return entries, err
}

// whitelistFs wraps a sandboxFs and allows access to specific paths outside
// the workspace when they match any of the provided patterns.
type whitelistFs struct {
	sandbox  *sandboxFs
	host     hostFs
	patterns []*regexp.Regexp
}

func (w *whitelistFs) matches(path string) bool {
	for _, p := range w.patterns {
		if p.MatchString(path) {
			return true
		}
	}
	return false
}

func (w *whitelistFs) ReadFile(path string) ([]byte, error) {
	if w.matches(path) {
		return w.host.ReadFile(path)
	}
	return w.sandbox.ReadFile(path)
}

func (w *whitelistFs) ReadFileRange(path string, offset, length int64) ([]byte, error) {
	if w.matches(path) {
		return w.host.ReadFileRange(path, offset, length)
	}
	return w.sandbox.ReadFileRange(path, offset, length)
}

func (w *whitelistFs) Stat(path string) (os.FileInfo, error) {
	if w.matches(path) {
		return w.host.Stat(path)
	}
	return w.sandbox.Stat(path)
}

func (w *whitelistFs) WriteFile(path string, data []byte) error {
	if w.matches(path) {
		return w.host.WriteFile(path, data)
	}
	return w.sandbox.WriteFile(path, data)
}

func (w *whitelistFs) ReadDir(path string) ([]os.DirEntry, error) {
	if w.matches(path) {
		return w.host.ReadDir(path)
	}
	return w.sandbox.ReadDir(path)
}

// buildFs returns the appropriate fileSystem implementation based on restriction
// settings and optional path whitelist patterns.
func buildFs(workspace string, restrict bool, patterns []*regexp.Regexp) fileSystem {
	if !restrict {
		return &hostFs{}
	}
	sandbox := &sandboxFs{workspace: workspace}
	if len(patterns) > 0 {
		return &whitelistFs{sandbox: sandbox, patterns: patterns}
	}
	return sandbox
}

// Helper to get a safe relative path for os.Root usage
func getSafeRelPath(workspace, path string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace is not defined")
	}

	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		var err error
		rel, err = filepath.Rel(workspace, rel)
		if err != nil {
			return "", fmt.Errorf("failed to calculate relative path: %w", err)
		}
	}

	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}

	return rel, nil
}

type EditFileTool struct {
	fs fileSystem
}

// NewEditFileTool creates a new EditFileTool with optional directory restriction.
func NewEditFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *EditFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &EditFileTool{fs: buildFs(workspace, restrict, patterns)}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Description() string {
	return "Edit a file by replacing old_text with new_text. " +
		"Input: path (string, required), old_text (string, required — must match exactly), new_text (string, required). " +
		"Output: confirmation of the edit. " +
		"The old_text must appear exactly once in the file. " +
		"If it appears multiple times, include more surrounding context to make it unique. " +
		"Use this instead of write_file when you only need to change part of a file."
}

func (t *EditFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The text to replace with",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return ErrorResult("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return ErrorResult("new_text is required")
	}

	if err := editFile(t.fs, path, oldText, newText); err != nil {
		return ErrorResult(err.Error())
	}
	return SilentResult(fmt.Sprintf("File edited: %s", path))
}

type AppendFileTool struct {
	fs fileSystem
}

func NewAppendFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *AppendFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &AppendFileTool{fs: buildFs(workspace, restrict, patterns)}
}

func (t *AppendFileTool) Name() string {
	return "append_file"
}

func (t *AppendFileTool) Description() string {
	return "Append content to the end of a file without modifying existing content. " +
		"Input: path (string, required), content (string, required). " +
		"Output: confirmation of the append. " +
		"Creates the file if it doesn't exist."
}

func (t *AppendFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to append to",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to append",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *AppendFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if err := appendFile(t.fs, path, content); err != nil {
		return ErrorResult(err.Error())
	}
	return SilentResult(fmt.Sprintf("Appended to %s", path))
}

// editFile reads the file via sysFs, performs the replacement, and writes back.
// It uses a fileSystem interface, allowing the same logic for both restricted and unrestricted modes.
func editFile(sysFs fileSystem, path, oldText, newText string) error {
	content, err := sysFs.ReadFile(path)
	if err != nil {
		return err
	}

	newContent, err := replaceEditContent(content, oldText, newText)
	if err != nil {
		return err
	}

	return sysFs.WriteFile(path, newContent)
}

// appendFile reads the existing content (if any) via sysFs, appends new content, and writes back.
func appendFile(sysFs fileSystem, path, appendContent string) error {
	content, err := sysFs.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	newContent := append(content, []byte(appendContent)...)
	return sysFs.WriteFile(path, newContent)
}

// replaceEditContent handles the core logic of finding and replacing a single occurrence of oldText.
func replaceEditContent(content []byte, oldText, newText string) ([]byte, error) {
	contentStr := string(content)

	if !strings.Contains(contentStr, oldText) {
		return nil, fmt.Errorf("old_text not found in file. Make sure it matches exactly, including whitespace and newlines. Suggestion: use read_file first to see the current file content, then copy the exact text to replace")
	}

	count := strings.Count(contentStr, oldText)
	if count > 1 {
		return nil, fmt.Errorf("old_text appears %d times. Please provide more context to make it unique", count)
	}

	newContent := strings.Replace(contentStr, oldText, newText, 1)
	return []byte(newContent), nil
}

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
