package utils

import (
	"os"
	"path/filepath"
)

const (
	// MediaTempDirName is the canonical temp directory name used for downloaded
	// media files. Keep this stable to simplify debugging and operational
	// cleanup.
	MediaTempDirName = "x_claw_media"
)

func MediaTempDir() string {
	return filepath.Join(os.TempDir(), MediaTempDirName)
}
