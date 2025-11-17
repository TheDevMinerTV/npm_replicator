package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-kivik/kivik/v4"
	_ "github.com/go-kivik/kivik/v4/couchdb" // The CouchDB driver
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
	"github.com/thedevminertv/npm-replicator/pkg/npm"
)

var (
	fCouchDBUsername = flag.String("couchdb-username", "", "CouchDB username")
	fCouchDBPassword = flag.String("couchdb-password", "", "CouchDB password")
	fCouchDBURL      = flag.String("couchdb-url", "http://127.0.0.1:5984", "CouchDB URL")
	fCouchDBDatabase = flag.String("couchdb-database", "registry", "CouchDB database")

	fLogLevel = flag.String("log-level", "info", "Log level")
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	flag.Parse()

	if *fCouchDBUsername != "" {
		if *fCouchDBPassword == "" {
			log.Fatal().Msg("CouchDB password is required if the username is set")
		}
	}
	if *fCouchDBURL == "" {
		log.Fatal().Msg("CouchDB URL is required")
	}
	if *fCouchDBDatabase == "" {
		log.Fatal().Msg("CouchDB database is required")
	}

	logLevel, err := zerolog.ParseLevel(*fLogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse log level")
	}
	zerolog.SetGlobalLevel(logLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(logLevel)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	npmClient := npm.New()

	couchdbURL, err := url.Parse(*fCouchDBURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse CouchDB URL")
	}
	if *fCouchDBUsername != "" {
		couchdbURL.User = url.UserPassword(*fCouchDBUsername, *fCouchDBPassword)
	}

	couchdbClient, err := kivik.New("couch", couchdbURL.String())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create CouchDB client")
	}

	if _, err := couchdbClient.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to ping CouchDB")
	}
	log.Info().Msg("CouchDB is alive")

	db := couchdbClient.DB(*fCouchDBDatabase)

	statusView := db.Query(
		ctx,
		"_design/npm_replication",
		"status",
		kivik.Param("limit", strconv.Itoa(10)),
		kivik.Param("include_docs", "true"),
		kivik.Param("key", "faulty"),
	)
	if err := statusView.Err(); err != nil {
		log.Error().Err(err).Msg("could not fetch faulty documents")
		return
	}

	for statusView.Next() {
		var packageID string
		if err := statusView.ScanValue(&packageID); err != nil {
			log.Error().Err(err).Msg("failed to read replicator status value")
			continue
		}

		var pkg RegistryPackage
		if err := statusView.ScanDoc(&pkg); err != nil {
			log.Error().Err(err).Msg("failed to read replicator document")
			continue
		}

		log := log.With().
			Str("package_id", packageID).
			Logger()

		log.Trace().Msg("Fetching metadata...")

		metadata, err := npmClient.PackageMetadata(ctx, packageID)
		if err != nil {
			log.Error().Err(err).Msg("Could not fetch package metadata")

			var httpErr httpclient.UnexpectedHttpStatusError
			if errors.As(err, &httpErr) && httpErr.StatusCode() == http.StatusNotFound {
				log.Error().Msg("Package not found in upstream registry, marking as bad document")

				pkg.Replicator.FoundInChangestreamButNotInRegistry = true

				// background context to make sure all of these are done before actually allowing the goroutine to exit
				newRev, err := db.Put(context.Background(), packageID, pkg)
				if err != nil {
					log.Error().Err(err).Msg("could not update replicator document")
					continue
				}

				log.Debug().
					Str("new_rev", newRev).
					Msg("refreshed metadata for package")
			}

			var jsonErr *json.UnmarshalTypeError
			if errors.As(err, &jsonErr) {
				log.Error().Err(err).Msg("Could not unmarshal JSON response from upstream registry")

				pkg.Replicator.HasJSONParseError = true

				// background context to make sure all of these are done before actually allowing the goroutine to exit
				newRev, err := db.Put(context.Background(), packageID, pkg)
				if err != nil {
					log.Error().Err(err).Msg("could not update replicator document")
					continue
				}

				log.Debug().
					Str("new_rev", newRev).
					Msg("refreshed metadata for package")
			}

			continue
		} else {
			var version npm.Version
			var hasInvalidTag = false

			if latestTag, ok := metadata.DistTags["latest"]; !ok {
				log.Warn().Interface("dist_tags", metadata.DistTags).Msg("Package has no latest tag")
				hasInvalidTag = true
			} else {
				latestVersion, ok := metadata.Versions[latestTag]
				if !ok {
					log.Warn().Str("latest_tag", latestTag).Msg("Latest tag is not a valid version")
					hasInvalidTag = true
				} else {
					version = latestVersion
				}
			}

			// generate new package document so that we don't have stale information in there
			pkg = RegistryPackage{
				Version: version,
				Rev_:    pkg.Rev_,
				Replicator: ReplicatorMetadata{
					UpstreamRev: pkg.Replicator.UpstreamRev,
					// mark the metadata as updated
					MetadataRev:   &pkg.Replicator.UpstreamRev,
					HasInvalidTag: hasInvalidTag,
				},
			}

			// background context to make sure all of these are done before actually allowing the goroutine to exit
			newRev, err := db.Put(context.Background(), packageID, pkg)
			if err != nil {
				log.Error().Err(err).Msg("could not update replicator document")
				continue
			}

			log.Debug().
				Str("new_rev", newRev).
				Msg("refreshed metadata for package")
		}
	}

	if statusView.Err() != nil {
		log.Panic().Err(statusView.Err()).Msg("failed to fetch replicator document statuses")
	}

	<-ctx.Done()
	cancel()
}

type ReplicatorMetadata struct {
	UpstreamRev          string     `json:"upstreamRev"`
	MetadataRev          *string    `json:"metadataRev"`
	DownloadsLastUpdated *time.Time `json:"downloadsLastUpdated"`

	FoundInChangestreamButNotInRegistry bool `json:"foundInChangestreamButNotInRegistry"`
	HasJSONParseError                   bool `json:"hasJSONParseError"`
	HasInvalidTag                       bool `json:"hasInvalidTag"`
}

type RegistryPackage struct {
	npm.Version

	Rev_ *string `json:"_rev,omitempty"`

	Replicator ReplicatorMetadata `json:"replicator"`
}
