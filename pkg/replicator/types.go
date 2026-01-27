package replicator

import (
	"time"

	"github.com/thedevminertv/npm-replicator/pkg/npm"
)

type ReplicatorMetadata struct {
	UpstreamRev             string     `json:"upstreamRev"`
	MetadataRev             *string    `json:"metadataRev"`
	DownloadsLastUpdated    *time.Time `json:"downloadsLastUpdated,omitempty"`
	DependentsLastUpdated   *time.Time `json:"dependentsLastUpdated,omitempty"`
	LastReplicatedAt        *time.Time `json:"lastReplicatedAt,omitempty"`

	FoundInChangestreamButNotInRegistry bool `json:"foundInChangestreamButNotInRegistry"`
	HasJSONParseError                   bool `json:"hasJSONParseError"`
	HasInvalidTag                       bool `json:"hasInvalidTag"`
}

type RegistryPackage struct {
	npm.Version

	Rev_ *string `json:"_rev,omitempty"`

	Replicator ReplicatorMetadata `json:"replicator"`
}
