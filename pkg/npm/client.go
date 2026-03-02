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
}

var defaultHeaders = httpclient.WithDefaultHeaders(http.Header{
	"User-Agent": []string{"npm-replicator (github.com/thedevminertv/npm-replicator)"},
})

func New() *Client {
	return &Client{
		replicateClient: httpclient.New(ReplicateBaseURL, defaultHeaders),
		registryClient:  httpclient.New(RegistryBaseURL, defaultHeaders),
		downloadsClient: httpclient.New(DownloadsBaseURL, defaultHeaders),
	}
}
