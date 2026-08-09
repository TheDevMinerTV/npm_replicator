package replicator

import (
	"time"

	"github.com/thedevminertv/npm-replicator/pkg/npm"
)

type ReplicatorMetadata struct {
	UpstreamRev          string     `json:"upstreamRev"`
	MetadataRev          *string    `json:"metadataRev"`
	DownloadsLastUpdated *time.Time `json:"downloadsLastUpdated"`

	// PackageType is the module kind derived from the latest version's type,
	// module and exports fields when the metadata was last refreshed. It is
	// omitted rather than written empty, because the changestream rewrites
	// documents it has no metadata for: absent means "never evaluated", which
	// is not the same as the evaluated-but-unclassifiable npm.PackageTypeUnknown.
	PackageType npm.PackageType `json:"packageType,omitempty"`

	FoundInChangestreamButNotInRegistry bool `json:"foundInChangestreamButNotInRegistry"`
	HasJSONParseError                   bool `json:"hasJSONParseError"`
	HasInvalidTag                       bool `json:"hasInvalidTag"`
}

type RegistryPackage struct {
	npm.Version

	Rev_ *string `json:"_rev,omitempty"`

	TarballSize *int64              `json:"tarballSize,omitempty"`
	Downloads   *npm.DownloadCounts `json:"downloads,omitempty"`
	Replicator  ReplicatorMetadata  `json:"replicator"`
}
