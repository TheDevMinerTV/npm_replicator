package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/rs/zerolog/log"
)

type ClientOpt func(*Client)

func WithCustomClient(client *http.Client) ClientOpt {
	return func(c *Client) {
		c.httpClient = client
	}
}

func WithDefaultHeaders(headers http.Header) ClientOpt {
	return func(c *Client) {
		c.baseHeaders = headers
	}
}

type Client struct {
	httpClient  *http.Client
	baseURL     string
	baseHeaders http.Header
}

func New(baseUrl string, opts ...ClientOpt) *Client {
	c := &Client{
		httpClient: &http.Client{},
		baseURL:    baseUrl,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) Get(ctx context.Context, path string, queryParams url.Values, headers http.Header) (io.ReadCloser, error) {
	return c.runRequest(ctx, http.MethodGet, path, queryParams, headers, nil)
}

func (c *Client) GetJSON(ctx context.Context, path string, queryParams url.Values, headers http.Header) (io.ReadCloser, error) {
	return c.Get(ctx, path, queryParams, http.Header{
		"Accept": []string{"application/json"},
	})
}

func (c *Client) Post(ctx context.Context, path string, queryParams url.Values, headers http.Header, body io.Reader) (io.ReadCloser, error) {
	return c.runRequest(ctx, http.MethodPost, path, queryParams, headers, body)
}

func (c *Client) PostJSON(ctx context.Context, path string, queryParams url.Values, body any) (io.ReadCloser, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	return c.Post(ctx, path, queryParams, http.Header{
		"Content-Type": []string{"application/json"},
	}, bytes.NewReader(rawBody))
}

func (c *Client) runRequest(ctx context.Context, method, path string, queryParams url.Values, headers http.Header, body io.Reader) (io.ReadCloser, error) {
	url, err := url.Parse(fmt.Sprintf("%s%s", c.baseURL, path))
	if err != nil {
		return nil, fmt.Errorf("failed to build request URL: %w", err)
	}

	url.RawQuery = queryParams.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, method, url.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	applyHeaders(httpReq.Header, c.baseHeaders)
	applyHeaders(httpReq.Header, headers)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		resBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("unexpected status code: %d, could not read body: %w", resp.StatusCode, err)
		}

		log.Error().
			Str("url", url.String()).
			Bytes("response_body", resBody).
			Msg("Could not query endpoint")

		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp.Body, nil
}

func applyHeaders(target http.Header, source http.Header) {
	for header, values := range source {
		h, ok := target[header]
		if !ok {
			target[header] = values
			continue
		}

		h = append(h, values...)
	}
}
