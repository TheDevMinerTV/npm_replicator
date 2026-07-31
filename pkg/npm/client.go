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
	downloadsHTTPClient *http.Client
	registryHTTPClient  *http.Client
	trafficRecorder     httpclient.TrafficRecorder
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
	o := &clientOptions{}
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
		replicateClient: httpclient.New(ReplicateBaseURL, defaultHeaders),
		registryClient:  httpclient.New(RegistryBaseURL, registryOpts...),
		downloadsClient: httpclient.New(DownloadsBaseURL, downloadsOpts...),
		tarballClient:   httpclient.New("", tarballOpts...),
	}
}
