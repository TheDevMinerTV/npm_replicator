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
	Event     string                     `json:"event"`
	Timestamp timestamp                  `json:"timestamp"`
	Package   replicator.RegistryPackage `json:"package"`
}

func NewMetadataUpdatedData(pkg replicator.RegistryPackage) MetadataUpdatedData {
	return MetadataUpdatedData{
		Event:     "metadata_updated",
		Timestamp: timestamp(time.Now()),
		Package:   pkg,
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
