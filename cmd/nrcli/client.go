package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is an HTTP client for the Notification Relay API with Basic Auth.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewClient creates a Client from the given config.
func NewClient(cfg *Config) *Client {
	return &Client{
		baseURL:  strings.TrimRight(cfg.URL, "/"),
		username: cfg.Username,
		password: cfg.Password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// APIError represents a non-2xx HTTP response.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// doRaw executes the request and returns (statusCode, bodyBytes, error).
// Responses with status >= 400 return an *APIError.
func (c *Client) doRaw(method, path string, query url.Values, reqBody any) (int, []byte, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := c.baseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequest(method, reqURL, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return resp.StatusCode, body, &APIError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
	}

	return resp.StatusCode, body, nil
}

// Get performs a GET request and returns the response body bytes.
func (c *Client) Get(path string, query url.Values) ([]byte, error) {
	_, body, err := c.doRaw(http.MethodGet, path, query, nil)
	return body, err
}

// Post performs a POST request with a JSON body and returns (statusCode, bodyBytes, error).
// reqBody may be nil to send no body.
func (c *Client) Post(path string, reqBody any) (int, []byte, error) {
	return c.doRaw(http.MethodPost, path, nil, reqBody)
}

// Put performs a PUT request with a JSON body and returns the response body bytes.
func (c *Client) Put(path string, reqBody any) ([]byte, error) {
	_, body, err := c.doRaw(http.MethodPut, path, nil, reqBody)
	return body, err
}

// Patch performs a PATCH request with a JSON body and returns (statusCode, bodyBytes, error).
func (c *Client) Patch(path string, reqBody any) (int, []byte, error) {
	return c.doRaw(http.MethodPatch, path, nil, reqBody)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string) error {
	_, _, err := c.doRaw(http.MethodDelete, path, nil, nil)
	return err
}
