package npm

import (
	"net/http"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

const (
	RegistryBaseURL  = "http://registry.npmjs.com"
	ReplicateBaseURL = "https://replicate.npmjs.com/registry"
)

type Client struct {
	replicateClient *httpclient.Client
	registryClient  *httpclient.Client
	downloadsClient *httpclient.Client
	tarballClient   *httpclient.Client
}

type ClientOpt func(*clientOptions)

type clientOptions struct {
	downloadsHTTPClient *http.Client
	registryHTTPClient  *http.Client
}

func WithRegistryHTTPClient(c *http.Client) ClientOpt {
	return func(o *clientOptions) {
		o.registryHTTPClient = c
	}
}

func WithDownloadsHTTPClient(c *http.Client) ClientOpt {
	return func(o *clientOptions) {
		o.downloadsHTTPClient = c
	}
}

var defaultHeaders = httpclient.WithDefaultHeaders(http.Header{
	"User-Agent": []string{"npm-replicator (github.com/thedevminertv/npm-replicator)"},
})

func New(opts ...ClientOpt) *Client {
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}

	registryOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.downloadsHTTPClient != nil {
		registryOpts = append(registryOpts, httpclient.WithCustomClient(o.registryHTTPClient))
	}

	downloadsOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.downloadsHTTPClient != nil {
		downloadsOpts = append(downloadsOpts, httpclient.WithCustomClient(o.downloadsHTTPClient))
	}

	return &Client{
		replicateClient: httpclient.New(ReplicateBaseURL, defaultHeaders),
		registryClient:  httpclient.New(RegistryBaseURL, registryOpts...),
		downloadsClient: httpclient.New(DownloadsBaseURL, downloadsOpts...),
		tarballClient:   httpclient.New("", defaultHeaders),
	}
}
