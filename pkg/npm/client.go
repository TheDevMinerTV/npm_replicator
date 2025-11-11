package npm

import (
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

const (
	RegistryBaseURL  = "https://registry.npmjs.com"
	ReplicateBaseURL = "https://replicate.npmjs.com/registry"
)

type Client struct {
	replicateClient *httpclient.Client
	registryClient  *httpclient.Client
}

func New() *Client {
	return &Client{
		replicateClient: httpclient.New(ReplicateBaseURL),
		registryClient:  httpclient.New(RegistryBaseURL),
	}
}
