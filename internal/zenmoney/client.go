package zenmoney

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.zenmoney.ru"

type API interface {
	Diff(context.Context, DiffRequest) (DiffResponse, error)
	Suggest(context.Context, []SuggestRequest) ([]SuggestResponse, error)
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(token string, httpClient *http.Client) *Client {
	return NewClientWithBaseURL(defaultAPIBase, token, httpClient)
}

func NewClientWithBaseURL(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: httpClient}
}

func (c *Client) Diff(ctx context.Context, request DiffRequest) (DiffResponse, error) {
	var response DiffResponse
	err := c.post(ctx, "/v8/diff/", request, &response)
	return response, err
}

func (c *Client) Suggest(ctx context.Context, request []SuggestRequest) ([]SuggestResponse, error) {
	var response []SuggestResponse
	err := c.post(ctx, "/v8/suggest/", request, &response)
	return response, err
}

func (c *Client) post(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode ZenMoney request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ZenMoney request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zenmoney-mcp-go/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call ZenMoney API: %w", err)
	}
	defer resp.Body.Close()

	const maxResponse = 64 << 20
	limited := io.LimitReader(resp.Body, maxResponse+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read ZenMoney response: %w", err)
	}
	if len(data) > maxResponse {
		return fmt.Errorf("ZenMoney response exceeds %d bytes", maxResponse)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		return fmt.Errorf("ZenMoney API returned HTTP %d: %s", resp.StatusCode, message)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode ZenMoney response: %w", err)
	}
	return nil
}
