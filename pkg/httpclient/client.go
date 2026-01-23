package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type StatusCodeCheckFn func(int) bool

func ExactStatusCode(expected int) StatusCodeCheckFn {
	return func(code int) bool {
		return code == expected
	}
}

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

func WithCustomLogger(logger zerolog.Logger) ClientOpt {
	return func(c *Client) {
		c.logger = logger
	}
}

func LogRequests() ClientOpt {
	return func(c *Client) {
		c.logRequests = true
	}
}

type Client struct {
	httpClient *http.Client

	logger      zerolog.Logger
	logRequests bool

	baseURL     string
	baseHeaders http.Header
}

func New(baseUrl string, opts ...ClientOpt) *Client {
	c := &Client{
		httpClient: &http.Client{},

		logger:      log.With().Str("component", "pkg/httpclient/Client").Logger(),
		logRequests: false,

		baseURL: baseUrl,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) Get(ctx context.Context, path string, queryParams url.Values, headers http.Header, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	return c.runRequest(ctx, http.MethodGet, path, queryParams, headers, nil, statusChecker)
}

func (c *Client) GetJSON(ctx context.Context, path string, queryParams url.Values, headers http.Header, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	return c.Get(ctx, path, queryParams, http.Header{
		"Accept": []string{"application/json"},
	}, statusChecker)
}

func (c *Client) Post(ctx context.Context, path string, queryParams url.Values, headers http.Header, body io.Reader, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	return c.runRequest(ctx, http.MethodPost, path, queryParams, headers, body, statusChecker)
}

func (c *Client) PostJSON(ctx context.Context, path string, queryParams url.Values, body any, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	return c.Post(ctx, path, queryParams, http.Header{
		"Content-Type": []string{"application/json"},
	}, bytes.NewReader(rawBody), statusChecker)
}

func (c *Client) Patch(ctx context.Context, path string, queryParams url.Values, headers http.Header, body io.Reader, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	return c.runRequest(ctx, http.MethodPatch, path, queryParams, headers, body, statusChecker)
}

func (c *Client) PatchJSON(ctx context.Context, path string, queryParams url.Values, body any, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	return c.Patch(ctx, path, queryParams, http.Header{
		"Content-Type": []string{"application/json"},
	}, bytes.NewReader(rawBody), statusChecker)
}

func (c *Client) runRequest(ctx context.Context, method, path string, queryParams url.Values, headers http.Header, reqBody io.Reader, statusChecker StatusCodeCheckFn) (io.Reader, error) {
	url, err := url.Parse(fmt.Sprintf("%s%s", c.baseURL, path))
	if err != nil {
		return nil, fmt.Errorf("failed to build request URL: %w", err)
	}

	url.RawQuery = queryParams.Encode()

	logger := c.logger.With().
		Str("url", url.String()).
		Str("method", method).
		Logger()

	var bodyBytes []byte
	if c.logRequests && reqBody != nil {
		bodyBytes, err = io.ReadAll(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body for logging: %w", err)
		}

		reqBody = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	applyHeaders(req.Header, c.baseHeaders)
	applyHeaders(req.Header, headers)

	if c.logRequests {
		ev := logger.Trace()

		if bodyBytes != nil {
			ev = ev.Bytes("request_body", bodyBytes)
		}

		for k, v := range req.Header {
			for i, vv := range v {
				ev = ev.Str(FormatHeaderAttribute("request", k, v, i), vv)
			}
		}

		ev.Msg("Running request")
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("Failed to send request: %w", err)
	}

	logger = logger.With().Dur("duration", duration).Int("statusCode", resp.StatusCode).Logger()

	defer resp.Body.Close()
	resBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		resBodyBytes = nil
	}
	resBody := bytes.NewBuffer(resBodyBytes)

	if c.logRequests {
		ev := logger.Trace().
			Bytes("response_body", resBodyBytes)

		for k, v := range resp.Header {
			for i, vv := range v {
				ev = ev.Str(FormatHeaderAttribute("response", k, v, i), vv)
			}
		}

		ev.Msg("Got response")
	}

	if !statusChecker(resp.StatusCode) {
		err = UnexpectedHTTPStatusCodeError{StatusCode: resp.StatusCode, Body: resBodyBytes}

		logger.Trace().
			Err(err).
			Msg("Failed not query endpoint")

		return nil, err
	}

	return resBody, nil
}

type UnexpectedHTTPStatusCodeError struct {
	StatusCode int
	Body       []byte
}

var _ error = (*UnexpectedHTTPStatusCodeError)(nil)

func (e UnexpectedHTTPStatusCodeError) Error() string {
	return fmt.Sprintf("unexpected status code: %d", e.StatusCode)
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

func FormatHeaderAttribute(t, k string, v []string, i int) string {
	attr := fmt.Sprintf("%s.headers.%s", t, k)

	if len(v) != 1 {
		attr = fmt.Sprintf("%s.%d", attr, i)
	}

	return attr
}
