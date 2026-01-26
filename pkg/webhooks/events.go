package webhooks

import (
	"encoding/json"
	"time"

	"github.com/thedevminertv/npm-replicator/pkg/replicator"
)

type timestamp time.Time

func (t timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).Unix())
}

type MetadataUpdatedData struct {
	Event           string                     `json:"event"`
	Timestamp       timestamp                  `json:"timestamp"`
	Package         replicator.RegistryPackage `json:"package"`
	Dependents      []DependentInfo            `json:"dependents,omitempty"`
	TotalDependents int                        `json:"totalDependents,omitempty"`
}

func NewMetadataUpdatedData(pkg replicator.RegistryPackage, dependents []DependentInfo, totalDependents int) MetadataUpdatedData {
	return MetadataUpdatedData{
		Event:           "metadata_updated",
		Timestamp:       timestamp(time.Now()),
		Package:         pkg,
		Dependents:      dependents,
		TotalDependents: totalDependents,
	}
}

type ChangestreamUpdatedData struct {
	Event     string                     `json:"event"`
	Timestamp timestamp                  `json:"timestamp"`
	Package   replicator.RegistryPackage `json:"package"`
}

func NewChangestreamUpdatedData(pkg replicator.RegistryPackage) ChangestreamUpdatedData {
	return ChangestreamUpdatedData{
		Event:     "changestream_updated",
		Timestamp: timestamp(time.Now()),
		Package:   pkg,
	}
}

// DependentInfo contains information about a package that depends on another.
type DependentInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}
