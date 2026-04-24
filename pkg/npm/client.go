package npm

import (
	"net/http"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

const (
	RegistryBaseURL  = "https://registry.npmjs.com"
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

	downloadsOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.downloadsHTTPClient != nil {
		downloadsOpts = append(downloadsOpts, httpclient.WithCustomClient(o.downloadsHTTPClient))
	}

	return &Client{
		replicateClient: httpclient.New(ReplicateBaseURL, defaultHeaders),
		registryClient:  httpclient.New(RegistryBaseURL, defaultHeaders),
		downloadsClient: httpclient.New(DownloadsBaseURL, downloadsOpts...),
		tarballClient:   httpclient.New("", defaultHeaders),
	}
}
