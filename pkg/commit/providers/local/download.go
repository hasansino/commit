package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const progressInterval = 5 * time.Second

const verificationStampVersion = 1

type verificationStamp struct {
	Version         int    `json:"version"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"mod_time_unix_nano"`
}

func (p *Local) resolveModel(ctx context.Context) (string, error) {
	if isHTTPURL(p.modelSource) {
		return p.downloadModel(ctx)
	}

	modelPath, err := filepath.Abs(p.modelSource)
	if err != nil {
		return "", fmt.Errorf("resolve model path: %w", err)
	}
	if err := validateModelFile(modelPath, p.expectedSHA256); err != nil {
		return "", err
	}
	return modelPath, nil
}

func (p *Local) downloadModel(ctx context.Context) (string, error) {
	modelURL, err := url.Parse(p.modelSource)
	if err != nil {
		return "", fmt.Errorf("parse model URL: %w", err)
	}
	filename := filepath.Base(modelURL.Path)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return "", fmt.Errorf("model URL does not contain a filename")
	}
	if err := validateChecksum(p.expectedSHA256); err != nil {
		return "", err
	}
	cacheDir, err := filepath.Abs(p.cacheDir)
	if err != nil {
		return "", fmt.Errorf("resolve model cache: %w", err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create model cache: %w", err)
	}

	destination := filepath.Join(cacheDir, filename)
	if _, err := os.Stat(destination); err == nil {
		if err := p.validateCachedModelFile(destination); err != nil {
			return "", fmt.Errorf("cached model is invalid: %w", err)
		}
		return destination, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect cached model: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelSource, nil)
	if err != nil {
		return "", fmt.Errorf("create model request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "commit-local-provider")
	if token := strings.TrimSpace(os.Getenv("HF_TOKEN")); token != "" &&
		isHuggingFaceHost(modelURL.Hostname()) {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	p.logger.InfoContext(ctx, "Downloading local model", "model", filename)
	response, err := p.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, response.Body, 4*1024)
		return "", fmt.Errorf("download model: unexpected HTTP status %s", response.Status)
	}

	tempFile, err := os.CreateTemp(cacheDir, "."+filename+"-*.part")
	if err != nil {
		return "", fmt.Errorf("create model temporary file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	hasher := sha256.New()
	written, copyErr := copyWithProgress(
		ctx,
		io.MultiWriter(tempFile, hasher),
		response.Body,
		response.ContentLength,
		p.logger,
		filename,
	)
	if copyErr != nil {
		_ = tempFile.Close()
		return "", fmt.Errorf("download model content: %w", copyErr)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		_ = tempFile.Close()
		return "", fmt.Errorf(
			"download model content: received %d bytes, expected %d",
			written,
			response.ContentLength,
		)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return "", fmt.Errorf("sync downloaded model: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("close downloaded model: %w", err)
	}

	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if p.expectedSHA256 != "" && actualChecksum != p.expectedSHA256 {
		return "", fmt.Errorf(
			"model checksum mismatch: got %s, expected %s",
			actualChecksum,
			p.expectedSHA256,
		)
	}
	if err := validateGGUF(tempPath); err != nil {
		return "", err
	}

	if err := os.Rename(tempPath, destination); err != nil {
		// Another process may have completed the same atomic download first.
		if validateErr := p.validateCachedModelFile(destination); validateErr == nil {
			return destination, nil
		}
		return "", fmt.Errorf("install downloaded model: %w", err)
	}
	p.recordModelVerification(destination)
	p.logger.InfoContext(
		ctx,
		"Downloaded local model",
		"model", filename,
		"size_bytes", written,
		"path", destination,
	)
	return destination, nil
}

func (p *Local) validateCachedModelFile(path string) error {
	info, err := inspectModelFile(path, p.expectedSHA256)
	if err != nil {
		return err
	}
	if p.expectedSHA256 == "" {
		return nil
	}
	if modelVerificationMatches(path, p.expectedSHA256, info) {
		return nil
	}
	if err := validateModelChecksum(path, p.expectedSHA256); err != nil {
		return err
	}
	p.recordModelVerification(path)
	return nil
}

func (p *Local) recordModelVerification(path string) {
	if p.expectedSHA256 == "" {
		return
	}
	info, err := os.Stat(path)
	if err == nil {
		err = writeVerificationStamp(path, p.expectedSHA256, info)
	}
	if err != nil && p.logger != nil {
		p.logger.Debug("Failed to cache local model verification", "path", path, "error", err)
	}
}

func verificationStampPath(modelPath string) string {
	return modelPath + ".verified.json"
}

func modelVerificationMatches(modelPath, expectedChecksum string, info os.FileInfo) bool {
	contents, err := os.ReadFile(verificationStampPath(modelPath))
	if err != nil {
		return false
	}
	var stamp verificationStamp
	if err := json.Unmarshal(contents, &stamp); err != nil {
		return false
	}
	return stamp.Version == verificationStampVersion &&
		stamp.SHA256 == expectedChecksum &&
		stamp.Size == info.Size() &&
		stamp.ModTimeUnixNano == info.ModTime().UnixNano()
}

func writeVerificationStamp(modelPath, checksum string, info os.FileInfo) error {
	stamp := verificationStamp{
		Version:         verificationStampVersion,
		SHA256:          checksum,
		Size:            info.Size(),
		ModTimeUnixNano: info.ModTime().UnixNano(),
	}
	contents, err := json.Marshal(stamp)
	if err != nil {
		return fmt.Errorf("encode model verification stamp: %w", err)
	}

	stampPath := verificationStampPath(modelPath)
	tempFile, err := os.CreateTemp(filepath.Dir(stampPath), ".verification-*.part")
	if err != nil {
		return fmt.Errorf("create model verification stamp: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("protect model verification stamp: %w", err)
	}
	if _, err := tempFile.Write(contents); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write model verification stamp: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close model verification stamp: %w", err)
	}
	if err := os.Rename(tempPath, stampPath); err != nil {
		return fmt.Errorf("install model verification stamp: %w", err)
	}
	return nil
}

func copyWithProgress(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	total int64,
	logger *slog.Logger,
	filename string,
) (int64, error) {
	buffer := make([]byte, 256*1024)
	var written int64
	nextProgress := time.Now().Add(progressInterval)
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			outputCount, writeErr := destination.Write(buffer[:count])
			written += int64(outputCount)
			if writeErr != nil {
				return written, writeErr
			}
			if outputCount != count {
				return written, io.ErrShortWrite
			}
		}
		if time.Now().After(nextProgress) {
			logger.InfoContext(
				ctx,
				"Downloading local model",
				"model", filename,
				"downloaded_bytes", written,
				"total_bytes", total,
			)
			nextProgress = time.Now().Add(progressInterval)
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func validateModelFile(path, expectedChecksum string) error {
	if _, err := inspectModelFile(path, expectedChecksum); err != nil {
		return err
	}
	return validateModelChecksum(path, expectedChecksum)
}

func inspectModelFile(path, expectedChecksum string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect model %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("model %q is not a regular file", path)
	}
	if err := validateGGUF(path); err != nil {
		return nil, err
	}
	if err := validateChecksum(expectedChecksum); err != nil {
		return nil, err
	}
	return info, nil
}

func validateModelChecksum(path, expectedChecksum string) error {
	if expectedChecksum == "" {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open model for checksum: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("checksum model: %w", err)
	}
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf(
			"model checksum mismatch: got %s, expected %s",
			actualChecksum,
			expectedChecksum,
		)
	}
	return nil
}

func validateGGUF(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open model %q: %w", path, err)
	}
	defer file.Close()

	magic := make([]byte, 4)
	if _, err := io.ReadFull(file, magic); err != nil {
		return fmt.Errorf("read model header %q: %w", path, err)
	}
	if string(magic) != "GGUF" {
		return fmt.Errorf("model %q is not a GGUF file", path)
	}
	return nil
}

func validateChecksum(checksum string) error {
	if checksum == "" {
		return nil
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("LOCAL_MODEL_SHA256 must be a 64-character SHA-256 checksum")
	}
	return nil
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func isHuggingFaceHost(host string) bool {
	host = strings.ToLower(host)
	return host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co")
}
