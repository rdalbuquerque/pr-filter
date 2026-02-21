package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AzureBlobClient uploads blobs to Azure Blob Storage using the REST API
// with Shared Key authentication. No SDK dependency required.
type AzureBlobClient struct {
	AccountName string
	AccountKey  string
	Container   string
	httpClient  *http.Client
}

// NewAzureBlobClient creates a new client for Azure Blob Storage.
// accountKey may be empty — in that case, uploads will be skipped.
func NewAzureBlobClient(accountName, accountKey, container string) *AzureBlobClient {
	return &AzureBlobClient{
		AccountName: accountName,
		AccountKey:  accountKey,
		Container:   container,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Enabled returns true if the client has enough configuration to upload.
func (c *AzureBlobClient) Enabled() bool {
	return c.AccountName != "" && c.AccountKey != "" && c.Container != ""
}

// Upload puts a blob into the container. blobName is the path within the container
// (e.g. "prs.json"). data is the raw bytes to upload.
func (c *AzureBlobClient) Upload(ctx context.Context, blobName string, data []byte) error {
	if !c.Enabled() {
		return nil // silently skip when not configured
	}

	blobURL := fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s",
		c.AccountName, c.Container, blobName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, blobURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("x-ms-date", now)
	req.Header.Set("x-ms-version", "2024-11-04")
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	req.ContentLength = int64(len(data))

	// Sign the request
	authHeader, err := c.signRequest(req, len(data))
	if err != nil {
		return fmt.Errorf("sign request: %w", err)
	}
	req.Header.Set("Authorization", authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload blob %s: status %d: %s", blobName, resp.StatusCode, string(body))
	}

	return nil
}

// signRequest creates a SharedKey authorization header for the request.
func (c *AzureBlobClient) signRequest(req *http.Request, contentLength int) (string, error) {
	// Build the string to sign per Azure SharedKey spec
	stringToSign := strings.Join([]string{
		req.Method,                           // HTTP verb
		"",                                   // Content-Encoding
		"",                                   // Content-Language
		fmt.Sprintf("%d", contentLength),     // Content-Length
		"",                                   // Content-MD5
		req.Header.Get("Content-Type"),       // Content-Type
		"",                                   // Date
		"",                                   // If-Modified-Since
		"",                                   // If-Match
		"",                                   // If-None-Match
		"",                                   // If-Unmodified-Since
		"",                                   // Range
		c.canonicalizedHeaders(req),          // CanonicalizedHeaders
		c.canonicalizedResource(req),         // CanonicalizedResource
	}, "\n")

	// HMAC-SHA256
	key, err := base64.StdEncoding.DecodeString(c.AccountKey)
	if err != nil {
		return "", fmt.Errorf("decode account key: %w", err)
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("SharedKey %s:%s", c.AccountName, signature), nil
}

func (c *AzureBlobClient) canonicalizedHeaders(req *http.Request) string {
	var headers []string
	for key := range req.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-ms-") {
			headers = append(headers, lower)
		}
	}
	sort.Strings(headers)

	var parts []string
	for _, h := range headers {
		val := strings.TrimSpace(req.Header.Get(h))
		parts = append(parts, h+":"+val)
	}
	return strings.Join(parts, "\n")
}

func (c *AzureBlobClient) canonicalizedResource(req *http.Request) string {
	u := req.URL
	resource := "/" + c.AccountName + u.Path

	// Add query parameters sorted
	params := u.Query()
	if len(params) > 0 {
		var keys []string
		for k := range params {
			keys = append(keys, strings.ToLower(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals := params[k]
			sort.Strings(vals)
			resource += "\n" + k + ":" + url.QueryEscape(strings.Join(vals, ","))
		}
	}

	return resource
}

// PublicURL returns the public URL for a blob in the container.
func (c *AzureBlobClient) PublicURL(blobName string) string {
	return fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s",
		c.AccountName, c.Container, blobName)
}
