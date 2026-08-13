package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultTransferMaxBytes = int64(200 * 1024 * 1024)
	maxErrorResponseBytes   = int64(1024 * 1024)
)

type requestBodyOpener func() (io.ReadCloser, int64, error)

type compiledMultipartPart struct {
	Name  string                 `json:"name"`
	Value *string                `json:"value,omitempty"`
	File  *compiledMultipartFile `json:"file,omitempty"`
}

type compiledMultipartFile struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	MaxBytes    int64  `json:"max_bytes"`
}

type compiledDownload struct {
	Path           string `json:"path"`
	Overwrite      any    `json:"overwrite"`
	MaxBytes       int64  `json:"max_bytes"`
	overwriteValue bool
}

type multipartBody struct {
	reader  io.Reader
	closers []io.Closer
}

func (body *multipartBody) Read(buffer []byte) (int, error) {
	return body.reader.Read(buffer)
}

func (body *multipartBody) Close() error {
	var firstErr error
	for _, closer := range body.closers {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	body.closers = nil
	return firstErr
}

func staticBodyOpener(content []byte) requestBodyOpener {
	stable := append([]byte(nil), content...)
	return func() (io.ReadCloser, int64, error) {
		if len(stable) == 0 {
			return http.NoBody, 0, nil
		}
		return io.NopCloser(bytes.NewReader(stable)), int64(len(stable)), nil
	}
}

func compileMultipart(ctx context.Context, res resolver, parts []multipartPart) ([]compiledMultipartPart, requestBodyOpener, string, error) {
	compiled := make([]compiledMultipartPart, 0, len(parts))
	for index, part := range parts {
		item := compiledMultipartPart{Name: part.Name}
		if part.Value != nil {
			resolved, err := res.resolveAny(ctx, part.Value)
			if err != nil {
				return nil, nil, "", err
			}
			value, err := stringifyFormValue(resolved)
			if err != nil {
				return nil, nil, "", fmt.Errorf("%w: multipart part %d value: %v", ErrConfig, index, err)
			}
			item.Value = &value
		} else {
			resolved, err := res.resolveAny(ctx, part.File)
			if err != nil {
				return nil, nil, "", err
			}
			pathValue, err := stringifyScalar(resolved)
			if err != nil {
				return nil, nil, "", fmt.Errorf("%w: multipart part %d file: %v", ErrConfig, index, err)
			}
			pathValue, err = expandPath(pathValue)
			if err != nil {
				return nil, nil, "", fmt.Errorf("%w: expand multipart path: %v", ErrExecution, err)
			}
			filename := filepath.Base(pathValue)
			if part.Filename != nil {
				resolvedFilename, resolveErr := res.resolveAny(ctx, part.Filename)
				if resolveErr != nil {
					return nil, nil, "", resolveErr
				}
				filename, resolveErr = stringifyScalar(resolvedFilename)
				if resolveErr != nil {
					return nil, nil, "", fmt.Errorf("%w: multipart part %d filename: %v", ErrConfig, index, resolveErr)
				}
			}
			if err := validateMultipartFilename(filename); err != nil {
				return nil, nil, "", fmt.Errorf("%w: multipart part %d filename: %v", ErrConfig, index, err)
			}
			contentType := strings.TrimSpace(part.ContentType)
			if contentType == "" {
				contentType = "application/octet-stream"
			} else if _, _, err := mime.ParseMediaType(contentType); err != nil {
				return nil, nil, "", fmt.Errorf("%w: multipart part %d content_type: %v", ErrConfig, index, err)
			}
			maxBytes := part.MaxBytes
			if maxBytes == 0 {
				maxBytes = defaultTransferMaxBytes
			}
			item.File = &compiledMultipartFile{Path: pathValue, Filename: filename, ContentType: contentType, MaxBytes: maxBytes}
		}
		compiled = append(compiled, item)
	}

	boundary, err := newMultipartBoundary()
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: create multipart boundary: %v", ErrExecution, err)
	}
	opener := func() (io.ReadCloser, int64, error) {
		return openMultipartBody(boundary, compiled)
	}
	return compiled, opener, "multipart/form-data; boundary=" + boundary, nil
}

func compileDownload(ctx context.Context, res resolver, raw *downloadConfig, hidden bool) (*compiledDownload, error) {
	if raw == nil {
		return nil, nil
	}
	resolvedPath, err := res.resolveAny(ctx, raw.Path)
	if err != nil {
		return nil, err
	}
	pathValue, err := stringifyScalar(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("%w: download path: %v", ErrConfig, err)
	}
	if !hidden || pathValue != redactedValue {
		pathValue, err = expandPath(pathValue)
		if err != nil {
			return nil, fmt.Errorf("%w: expand download path: %v", ErrExecution, err)
		}
		pathValue = filepath.Clean(pathValue)
	}

	overwriteDisplay := any(false)
	overwriteValue := false
	if raw.Overwrite != nil {
		resolvedOverwrite, resolveErr := res.resolveAny(ctx, raw.Overwrite)
		if resolveErr != nil {
			return nil, resolveErr
		}
		overwriteDisplay = resolvedOverwrite
		if !(hidden && resolvedOverwrite == redactedValue) {
			value, ok := resolvedOverwrite.(bool)
			if !ok {
				return nil, fmt.Errorf("%w: download overwrite must resolve to a boolean, got %T", ErrConfig, resolvedOverwrite)
			}
			overwriteValue = value
		}
	}
	maxBytes := raw.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultTransferMaxBytes
	}
	return &compiledDownload{Path: pathValue, Overwrite: overwriteDisplay, MaxBytes: maxBytes, overwriteValue: overwriteValue}, nil
}

func validateMultipartFilename(filename string) error {
	if filename == "" || filename == "." {
		return fmt.Errorf("must not be empty")
	}
	if filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\\`) {
		return fmt.Errorf("must not contain path separators")
	}
	if hasControlCharacter(filename) {
		return fmt.Errorf("must not contain control characters")
	}
	return nil
}

func newMultipartBoundary() (string, error) {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "httpx-" + hex.EncodeToString(random), nil
}

func openMultipartBody(boundary string, parts []compiledMultipartPart) (io.ReadCloser, int64, error) {
	readers := make([]io.Reader, 0, len(parts)*2+1)
	closers := make([]io.Closer, 0, len(parts))
	var total int64
	closeFiles := func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}
	appendBytes := func(content []byte) error {
		if int64(len(content)) > int64(^uint64(0)>>1)-total {
			return fmt.Errorf("multipart content length overflow")
		}
		readers = append(readers, bytes.NewReader(content))
		total += int64(len(content))
		return nil
	}

	for index, part := range parts {
		prefix := "--" + boundary + "\r\n"
		if index > 0 {
			prefix = "\r\n" + prefix
		}
		dispositionParams := map[string]string{"name": part.Name}
		headers := map[string]string{}
		var dataReader io.Reader
		var dataLength int64
		if part.File != nil {
			dispositionParams["filename"] = part.File.Filename
			headers["Content-Type"] = part.File.ContentType
			file, err := os.Open(part.File.Path)
			if err != nil {
				closeFiles()
				return nil, 0, fmt.Errorf("%w: open multipart file %q: %v", ErrExecution, part.File.Path, pathErrorCause(err))
			}
			info, err := file.Stat()
			if err != nil {
				_ = file.Close()
				closeFiles()
				return nil, 0, fmt.Errorf("%w: stat multipart file %q: %v", ErrExecution, part.File.Path, pathErrorCause(err))
			}
			if !info.Mode().IsRegular() {
				_ = file.Close()
				closeFiles()
				return nil, 0, fmt.Errorf("%w: multipart file %q is not a regular file", ErrExecution, part.File.Path)
			}
			if info.Size() > part.File.MaxBytes {
				_ = file.Close()
				closeFiles()
				return nil, 0, fmt.Errorf("%w: multipart file %q exceeds max_bytes (%d > %d)", ErrExecution, part.File.Path, info.Size(), part.File.MaxBytes)
			}
			closers = append(closers, file)
			dataReader = file
			dataLength = info.Size()
		} else {
			data := []byte(*part.Value)
			dataReader = bytes.NewReader(data)
			dataLength = int64(len(data))
		}
		headers["Content-Disposition"] = mime.FormatMediaType("form-data", dispositionParams)
		headerBlock := buildMultipartHeader(prefix, headers)
		if err := appendBytes(headerBlock); err != nil {
			closeFiles()
			return nil, 0, fmt.Errorf("%w: %v", ErrExecution, err)
		}
		if dataLength > int64(^uint64(0)>>1)-total {
			closeFiles()
			return nil, 0, fmt.Errorf("%w: multipart content length overflow", ErrExecution)
		}
		readers = append(readers, dataReader)
		total += dataLength
	}
	if err := appendBytes([]byte("\r\n--" + boundary + "--\r\n")); err != nil {
		closeFiles()
		return nil, 0, fmt.Errorf("%w: %v", ErrExecution, err)
	}
	return &multipartBody{reader: io.MultiReader(readers...), closers: closers}, total, nil
}

func buildMultipartHeader(prefix string, headers map[string]string) []byte {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buffer strings.Builder
	buffer.WriteString(prefix)
	for _, key := range keys {
		buffer.WriteString(key)
		buffer.WriteString(": ")
		buffer.WriteString(headers[key])
		buffer.WriteString("\r\n")
	}
	buffer.WriteString("\r\n")
	return []byte(buffer.String())
}

func preflightDownload(download *compiledDownload) error {
	if download == nil {
		return nil
	}
	if strings.TrimSpace(download.Path) == "" {
		return fmt.Errorf("%w: download path resolved to an empty string", ErrExecution)
	}
	parent := filepath.Dir(download.Path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("%w: download parent directory %q: %v", ErrExecution, parent, pathErrorCause(err))
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: download parent %q is not a directory", ErrExecution, parent)
	}
	target, err := os.Lstat(download.Path)
	if err == nil {
		if target.IsDir() {
			return fmt.Errorf("%w: download target %q is a directory", ErrExecution, download.Path)
		}
		if !download.overwriteValue {
			return fmt.Errorf("%w: download target %q already exists; set overwrite=true to replace it", ErrExecution, download.Path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%w: inspect download target %q: %v", ErrExecution, download.Path, pathErrorCause(err))
	}
	return nil
}

func (rt *Runtime) performDownload(req commandRequest, compiled *compiledRequest, response *http.Response, durationMS int64) (requestOutcome, bool, error) {
	headers := cloneHeaders(response.Header)
	ok := matchesExpectedStatus(response.StatusCode, compiled.ExpectStatus)
	if response.StatusCode >= 500 && compiled.Retries > 0 && !ok {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorResponseBytes))
		return requestOutcome{}, true, fmt.Errorf("%w: received status %d", ErrExecution, response.StatusCode)
	}
	if !ok {
		bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, maxErrorResponseBytes+1))
		if err != nil {
			return requestOutcome{}, false, fmt.Errorf("%w: read error response body: %v", ErrExecution, err)
		}
		if int64(len(bodyBytes)) > maxErrorResponseBytes {
			bodyBytes = bodyBytes[:maxErrorResponseBytes]
		}
		env := envelope{OK: false, Site: req.Site, Action: compiled.Action, Status: response.StatusCode, DurationMS: durationMS, Headers: headers, Body: decodeResponseBody(response, bodyBytes), Error: &errorEnvelope{Code: "assertion_error", Message: fmt.Sprintf("unexpected status %d", response.StatusCode)}}
		return requestOutcome{Envelope: env, RawBody: bodyBytes, ExitCode: ExitAssertion}, false, fmt.Errorf("%w: unexpected status %d", ErrAssertion, response.StatusCode)
	}

	download := compiled.Download
	if response.ContentLength > download.MaxBytes {
		return requestOutcome{}, false, fmt.Errorf("%w: download exceeds max_bytes (%d > %d)", ErrExecution, response.ContentLength, download.MaxBytes)
	}
	temp, err := os.CreateTemp(filepath.Dir(download.Path), "."+filepath.Base(download.Path)+".httpx-*")
	if err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: create temporary download file: %v", ErrExecution, pathErrorCause(err))
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	hasher := sha256.New()
	written, copyErr := copyDownload(temp, hasher, response.Body, download.MaxBytes)
	if copyErr != nil {
		_ = temp.Close()
		return requestOutcome{}, true, copyErr
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return requestOutcome{}, false, fmt.Errorf("%w: sync temporary download file: %v", ErrExecution, pathErrorCause(err))
	}
	if err := temp.Close(); err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: close temporary download file: %v", ErrExecution, pathErrorCause(err))
	}
	if err := publishDownloadedFile(tempPath, download.Path, download.overwriteValue); err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: publish download to %q: %v", ErrExecution, download.Path, pathErrorCause(err))
	}
	keepTemp = false
	result := &downloadResult{Path: download.Path, SizeBytes: written, SHA256: hex.EncodeToString(hasher.Sum(nil)), ContentType: response.Header.Get("Content-Type")}
	env := envelope{OK: true, Site: req.Site, Action: compiled.Action, Status: response.StatusCode, DurationMS: durationMS, Headers: headers, Download: result}
	return requestOutcome{Envelope: env, RawBody: []byte(download.Path), ExitCode: ExitSuccess}, false, nil
}

func copyDownload(destination io.Writer, hasher hash.Hash, source io.Reader, maxBytes int64) (int64, error) {
	written, err := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(source, maxBytes+1))
	if err != nil {
		return written, fmt.Errorf("%w: read download response: %v", ErrExecution, err)
	}
	if written > maxBytes {
		return written, fmt.Errorf("%w: download exceeds max_bytes (%d > %d)", ErrExecution, written, maxBytes)
	}
	return written, nil
}
