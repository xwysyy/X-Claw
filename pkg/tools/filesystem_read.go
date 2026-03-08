package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

type ReadFileTool struct {
	fs           fileSystem
	maxReadBytes int
}

func firstAllowPaths(allowPaths ...[]*regexp.Regexp) []*regexp.Regexp {
	if len(allowPaths) == 0 {
		return nil
	}
	return allowPaths[0]
}

func buildToolFS(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) fileSystem {
	return buildFs(workspace, restrict, firstAllowPaths(allowPaths...))
}

func NewReadFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ReadFileTool {
	return &ReadFileTool{
		fs:           buildToolFS(workspace, restrict, allowPaths...),
		maxReadBytes: 30_000,
	}
}

func parseReadFileRequest(args map[string]any, defaultMaxBytes int) (string, int64, int, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", 0, 0, errors.New("path is required")
	}

	maxBytes := defaultMaxBytes
	if maxBytes <= 0 {
		maxBytes = 30_000
	}
	parsedMax, err := parseOptionalIntArg(args, "max_bytes", maxBytes, 200, 5*1024*1024)
	if err != nil {
		return "", 0, 0, err
	}

	offset, err := parseOptionalIntArg(args, "offset", 0, 0, 1_000_000_000)
	if err != nil {
		return "", 0, 0, err
	}

	return path, int64(offset), parsedMax, nil
}

func (t *ReadFileTool) readResult(path string, off int64, maxBytes int) *ToolResult {
	fi, statErr := t.fs.Stat(path)
	if statErr != nil || fi == nil {
		buf, readErr := t.fs.ReadFileRange(path, off, int64(maxBytes))
		if readErr != nil {
			return ErrorResult(readErr.Error()).WithError(readErr)
		}
		return NewToolResult(string(buf))
	}

	size := fi.Size()
	if size < 0 {
		size = 0
	}
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
	maxReadBytes := 0
	if t != nil {
		maxReadBytes = t.maxReadBytes
	}
	path, offset, maxBytes, err := parseReadFileRequest(args, maxReadBytes)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	if t == nil || t.fs == nil {
		return ErrorResult("filesystem is not configured")
	}
	return t.readResult(path, offset, maxBytes)
}
