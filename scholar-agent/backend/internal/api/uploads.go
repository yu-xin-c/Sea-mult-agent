package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const uploadTextExcerptLimit = 64 * 1024

type uploadMetadata struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	OwnerID     string    `json:"owner_id"`
	SessionID   string    `json:"session_id"`
	StoredPath  string    `json:"stored_path"`
	CreatedAt   time.Time `json:"created_at"`
}

type uploadResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ContentURL  string    `json:"content_url"`
	CreatedAt   time.Time `json:"created_at"`
}

func RegisterUploadRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/uploads", createUpload)
	apiGroup.GET("/uploads/:id/content", serveUploadContent)
}

func createUpload(c *gin.Context) {
	maxBytes := int64(positiveEnvInt("UPLOAD_MAX_MB", 32)) * 1024 * 1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+1024*1024)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart field 'file' is required"})
		return
	}
	if fileHeader.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded file is empty"})
		return
	}
	if fileHeader.Size > maxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "uploaded file exceeds configured size limit"})
		return
	}

	name := sanitizeUploadName(fileHeader.Filename)
	extension := strings.ToLower(filepath.Ext(name))
	if !allowedUploadExtension(extension) {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported file type"})
		return
	}

	source, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read uploaded file"})
		return
	}
	defer source.Close()

	prefix := make([]byte, 512)
	readCount, readErr := io.ReadFull(source, prefix)
	if readErr != nil && readErr != io.ErrUnexpectedEOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot inspect uploaded file"})
		return
	}
	prefix = prefix[:readCount]
	contentType := http.DetectContentType(prefix)
	if !allowedUploadContent(extension, contentType) {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "file content does not match its extension"})
		return
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot rewind uploaded file"})
		return
	}

	ownerID := resolveUserID(c)
	sessionID := resolveSessionID(c)
	uploadID := uuid.NewString()
	directory := uploadDirectory(ownerID, uploadID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot create upload directory"})
		return
	}
	storedPath := filepath.Join(directory, "content"+extension)
	metadata, err := persistUpload(source, fileHeader, storedPath, uploadMetadata{
		ID:          uploadID,
		Name:        name,
		ContentType: normalizedUploadContentType(extension, contentType),
		OwnerID:     ownerID,
		SessionID:   sessionID,
		StoredPath:  storedPath,
		CreatedAt:   time.Now().UTC(),
	}, maxBytes)
	if err != nil {
		_ = os.RemoveAll(directory)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := writeUploadMetadata(metadata); err != nil {
		_ = os.RemoveAll(directory)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot persist upload metadata"})
		return
	}

	c.JSON(http.StatusCreated, uploadResponseFromMetadata(metadata))
}

func persistUpload(source multipart.File, header *multipart.FileHeader, path string, metadata uploadMetadata, maxBytes int64) (uploadMetadata, error) {
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return metadata, fmt.Errorf("cannot create upload file")
	}
	defer destination.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(source, maxBytes+1))
	if err != nil {
		return metadata, fmt.Errorf("cannot save uploaded file")
	}
	if written > maxBytes || written != header.Size {
		return metadata, fmt.Errorf("uploaded file size changed during transfer")
	}
	metadata.Size = written
	metadata.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return metadata, nil
}

func serveUploadContent(c *gin.Context) {
	metadata, err := loadOwnedUpload(resolveUserID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", metadata.Name))
	c.Header("X-Content-Type-Options", "nosniff")
	c.File(metadata.StoredPath)
}

func resolvePlanUploads(ownerID string, uploadIDs []string) ([]map[string]any, error) {
	if len(uploadIDs) > 8 {
		return nil, fmt.Errorf("at most 8 attachments are allowed per plan")
	}
	resolved := make([]map[string]any, 0, len(uploadIDs))
	seen := map[string]struct{}{}
	for _, uploadID := range uploadIDs {
		uploadID = strings.TrimSpace(uploadID)
		if uploadID == "" {
			continue
		}
		if _, exists := seen[uploadID]; exists {
			continue
		}
		seen[uploadID] = struct{}{}
		metadata, err := loadOwnedUpload(ownerID, uploadID)
		if err != nil {
			return nil, fmt.Errorf("attachment %q is unavailable", uploadID)
		}
		item := map[string]any{
			"id":           metadata.ID,
			"name":         metadata.Name,
			"content_type": metadata.ContentType,
			"size":         metadata.Size,
			"sha256":       metadata.SHA256,
			"storage_path": metadata.StoredPath,
		}
		if isTextUpload(filepath.Ext(metadata.Name)) {
			if raw, readErr := os.ReadFile(metadata.StoredPath); readErr == nil {
				if len(raw) > uploadTextExcerptLimit {
					raw = raw[:uploadTextExcerptLimit]
				}
				item["text_excerpt"] = string(raw)
			}
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func loadOwnedUpload(ownerID, uploadID string) (uploadMetadata, error) {
	if _, err := uuid.Parse(uploadID); err != nil {
		return uploadMetadata{}, err
	}
	metadataPath := filepath.Join(uploadDirectory(ownerID, uploadID), "metadata.json")
	raw, err := os.ReadFile(metadataPath)
	if err != nil {
		return uploadMetadata{}, err
	}
	var metadata uploadMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return uploadMetadata{}, err
	}
	if metadata.OwnerID != ownerID || metadata.ID != uploadID {
		return uploadMetadata{}, fmt.Errorf("upload ownership mismatch")
	}
	if info, err := os.Stat(metadata.StoredPath); err != nil || !info.Mode().IsRegular() {
		return uploadMetadata{}, fmt.Errorf("upload content is missing")
	}
	return metadata, nil
}

func writeUploadMetadata(metadata uploadMetadata) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(metadata.StoredPath), "metadata.json"), raw, 0o600)
}

func uploadResponseFromMetadata(metadata uploadMetadata) uploadResponse {
	return uploadResponse{
		ID: metadata.ID, Name: metadata.Name, ContentType: metadata.ContentType,
		Size: metadata.Size, SHA256: metadata.SHA256,
		ContentURL: "/api/uploads/" + metadata.ID + "/content", CreatedAt: metadata.CreatedAt,
	}
}

func uploadDirectory(ownerID, uploadID string) string {
	ownerHash := sha256.Sum256([]byte(ownerID))
	return filepath.Join(uploadRoot(), hex.EncodeToString(ownerHash[:16]), uploadID)
}

func uploadRoot() string {
	if configured := strings.TrimSpace(os.Getenv("UPLOAD_ROOT")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(os.TempDir(), "scholar-agent-uploads")
}

func sanitizeUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		return "attachment"
	}
	if len(name) > 180 {
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name, ext)[:160] + ext
	}
	return name
}

func allowedUploadExtension(extension string) bool {
	_, ok := map[string]struct{}{
		".pdf": {}, ".txt": {}, ".md": {}, ".json": {}, ".yaml": {}, ".yml": {},
		".toml": {}, ".py": {}, ".ipynb": {}, ".csv": {}, ".tsv": {}, ".jsonl": {},
	}[extension]
	return ok
}

func allowedUploadContent(extension, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if extension == ".pdf" {
		return contentType == "application/pdf"
	}
	return strings.HasPrefix(contentType, "text/") || contentType == "application/json"
}

func normalizedUploadContentType(extension, detected string) string {
	switch extension {
	case ".pdf":
		return "application/pdf"
	case ".json", ".jsonl", ".ipynb":
		return "application/json"
	default:
		return strings.TrimSpace(strings.Split(detected, ";")[0])
	}
}

func isTextUpload(extension string) bool {
	return strings.ToLower(extension) != ".pdf"
}
