//go:build !windows

package app

import (
	"os"
	"path/filepath"
)

func publishDownloadedFile(tempPath, targetPath string, overwrite bool) error {
	if overwrite {
		if err := os.Rename(tempPath, targetPath); err != nil {
			return err
		}
	} else {
		if err := os.Link(tempPath, targetPath); err != nil {
			return err
		}
		if err := os.Remove(tempPath); err != nil {
			return err
		}
	}
	return syncStateDirectory(filepath.Dir(targetPath))
}
