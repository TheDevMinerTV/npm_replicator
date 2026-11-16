package npm

import (
	"net/http"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

const (
	RegistryBaseURL  = "http://registry.npmjs.com"
	ReplicateBaseURL = "https://replicate.npmjs.com/registry"

	TrafficOperationMetadata       = "metadata"
	TrafficOperationPackageSize    = "package_size"
	TrafficOperationDownloadCounts = "download_counts"
)

type Client struct {
	replicateClient *httpclient.Client
	registryClient  *httpclient.Client
	downloadsClient *httpclient.Client
	tarballClient   *httpclient.Client
}

type ClientOpt func(*clientOptions)

type clientOptions struct {
	replicateBaseURL    string
	registryBaseURL     string
	downloadsHTTPClient *http.Client
	registryHTTPClient  *http.Client
	trafficRecorder     httpclient.TrafficRecorder
}

// WithBaseURLs points the replicate and registry clients somewhere other than
// npm, for tests that stand in for upstream.
func WithBaseURLs(replicate, registry string) ClientOpt {
	return func(o *clientOptions) {
		o.replicateBaseURL = replicate
		o.registryBaseURL = registry
	}
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

func WithTrafficRecorder(recorder httpclient.TrafficRecorder) ClientOpt {
	return func(o *clientOptions) {
		o.trafficRecorder = recorder
	}
}

var defaultHeaders = httpclient.WithDefaultHeaders(http.Header{
	"User-Agent": []string{"npm-replicator (github.com/thedevminertv/npm-replicator)"},
})

func New(opts ...ClientOpt) *Client {
	o := &clientOptions{
		replicateBaseURL: ReplicateBaseURL,
		registryBaseURL:  RegistryBaseURL,
	}
	for _, opt := range opts {
		opt(o)
	}

	registryOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.registryHTTPClient != nil {
		registryOpts = append(registryOpts, httpclient.WithCustomClient(o.registryHTTPClient))
	} else if o.trafficRecorder != nil {
		registryOpts = append(registryOpts, httpclient.WithCustomClient(
			httpclient.NewMeteredHTTPClient(TrafficOperationMetadata, o.trafficRecorder),
		))
	}

	downloadsOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.downloadsHTTPClient != nil {
		downloadsOpts = append(downloadsOpts, httpclient.WithCustomClient(o.downloadsHTTPClient))
	} else if o.trafficRecorder != nil {
		downloadsOpts = append(downloadsOpts, httpclient.WithCustomClient(
			httpclient.NewMeteredHTTPClient(TrafficOperationDownloadCounts, o.trafficRecorder),
		))
	}

	tarballOpts := []httpclient.ClientOpt{defaultHeaders}
	if o.trafficRecorder != nil {
		tarballOpts = append(tarballOpts, httpclient.WithCustomClient(
			httpclient.NewMeteredHTTPClient(TrafficOperationPackageSize, o.trafficRecorder),
		))
	}

	return &Client{
		replicateClient: httpclient.New(o.replicateBaseURL, defaultHeaders),
		registryClient:  httpclient.New(o.registryBaseURL, registryOpts...),
		downloadsClient: httpclient.New(DownloadsBaseURL, downloadsOpts...),
		tarballClient:   httpclient.New("", tarballOpts...),
	}
}
