package app

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var dataURLMediaTypeByExtension = map[string]string{
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
}

func encodeFileDataURL(path string, maxBytes int64, allowedMediaTypes []string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: file_data_url path must be absolute", ErrExecution)
	}
	mediaType, ok := dataURLMediaTypeByExtension[strings.ToLower(filepath.Ext(path))]
	if !ok || !containsString(allowedMediaTypes, mediaType) {
		return "", fmt.Errorf("%w: file_data_url media type is not allowed", ErrExecution)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: open file_data_url path %q: %v", ErrExecution, path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: stat file_data_url path %q: %v", ErrExecution, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: file_data_url path must be a regular file", ErrExecution)
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return "", fmt.Errorf("%w: file_data_url file size must be between 1 and %d bytes", ErrExecution, maxBytes)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: read file_data_url path %q: %v", ErrExecution, path, err)
	}
	if len(content) == 0 || int64(len(content)) > maxBytes {
		return "", fmt.Errorf("%w: file_data_url file size must be between 1 and %d bytes", ErrExecution, maxBytes)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(content), nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
