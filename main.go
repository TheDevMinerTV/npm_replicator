package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-kivik/kivik/v4"
	_ "github.com/go-kivik/kivik/v4/couchdb" // The CouchDB driver
	"github.com/go-kivik/kivik/v4/couchdb/chttp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
	"github.com/thedevminertv/npm-replicator/pkg/npm"
)

var (
	localDBActiveSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "couchdb",
			Name:      "active_size",
			Help:      "Active database size according to CouchDB",
		},
	)
	localDBSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "couchdb",
			Name:      "size",
			Help:      "On disk database size according to CouchDB",
		},
	)
	localDocumentCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "events",
			Name:      "document_count",
			Help:      "Amount of documents in the replicator",
		},
		[]string{"status"},
	)
	localLastSyncedSequenceID = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "events",
			Name:      "last_synced_sequence_id",
			Help:      "Last sequence number synced",
		},
	)

	upstreamDocumentCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "upstream",
			Name:      "document_count",
			Help:      "Amount of documents in upstream",
		},
	)
	upstreamSequenceID = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "upstream",
			Name:      "sequence_id",
			Help:      "Last sequence number in upstream",
		},
	)
)

var (
	fCouchDBUsername = flag.String("couchdb-username", "", "CouchDB username")
	fCouchDBPassword = flag.String("couchdb-password", "", "CouchDB password")
	fCouchDBURL      = flag.String("couchdb-url", "http://127.0.0.1:5984", "CouchDB URL")
	fCouchDBDatabase = flag.String("couchdb-database", "registry", "CouchDB database")

	fTrackerFile       = flag.String("tracker-file", "tracker.json", "Tracker file used for keeping the latest changestream offset")
	fLogLevel          = flag.String("log-level", "info", "Log level")
	fMetricsListenAddr = flag.String("metrics-listen-addr", "", "Metrics listen address (disabled if empty)")

	fStatsUpdateInterval = flag.Duration("stat-update-interval", 30*time.Second, "Interval between stats updates")

	fChangestreamFetchInterval = flag.Duration("changes-fetch-interval", 10*time.Second, "Interval between changes fetches")
	fChangestreamBatchSize     = flag.Int("changes-batch-size", 1000, "Batch size for changes fetches")

	fBatchedMetadataUpdateInterval = flag.Duration("metadata-update-interval", 15*time.Second, "Interval between batched metadata updates")
	fMetadataUpdateBatchSize       = flag.Int("metadata-update-batch-size", 10, "Batch size for metadata updates")
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

	if *fTrackerFile == "" {
		log.Fatal().Msg("Tracker file is required")
	}

	logLevel, err := zerolog.ParseLevel(*fLogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse log level")
	}
	zerolog.SetGlobalLevel(logLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(logLevel)

	prometheus.MustRegister(
		localDBActiveSize,
		localDBSize,
		localDocumentCount,
		localLastSyncedSequenceID,
		upstreamDocumentCount,
		upstreamSequenceID,
	)
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

	tracker, err := NewTracker(*fTrackerFile, TrackerData{
		LastSeenSeq: 0,
	})
	if err != nil {
		panic(err)
	}
	defer tracker.Close()
	log.Info().Msg("Tracker is ready")

	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpServer := &http.Server{
		Addr:    *fMetricsListenAddr,
		Handler: httpMux,
	}

	// init stats so that there's no drop in the stats
	updateStats(ctx, npmClient, db)
	localLastSyncedSequenceID.Set(float64(tracker.LastSeq()))

	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.Tick(*fStatsUpdateInterval)
		log.Info().
			Dur("interval", *fStatsUpdateInterval).
			Msg("Starting stats updater")

		defer func() {
			if err := recover(); err != nil {
				log.Error().Interface("err", err).Msg("caught panic")
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker:
				updateStats(ctx, npmClient, db)
			}
		}
	})

	wg.Go(func() {
		ticker := time.Tick(*fChangestreamFetchInterval)
		log.Info().
			Dur("interval", *fChangestreamFetchInterval).
			Int("batch_size", *fChangestreamBatchSize).
			Msg("Starting changestream fetcher")

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker:
				eventCountDifference := _upstreamSequenceID - tracker.LastSeq()
				if eventCountDifference < int64(*fChangestreamBatchSize) {
					log.Trace().
						Int64("event_count_difference", eventCountDifference).
						Int("required_difference", *fChangestreamBatchSize).
						Msg("not fetching changes, because there are too few changes from upstream")
					continue
				}

				changes, err := npmClient.Changes(ctx, tracker.LastSeq(), *fChangestreamBatchSize)
				if err != nil {
					log.Panic().Err(err).Msg("could not fetch replicator changes from upstream")
				}

				for _, change := range changes.Results {
					// TODO: this should probably be done smarter
					upstreamRev := change.Changes[0].Rev

					log := log.With().
						Str("package_id", change.ID).
						Int64("sequence_id", change.Seq).
						Str("upstream_rev", upstreamRev).
						Logger()

					var pkg RegistryPackage

					existingDoc := db.Get(ctx, change.ID)
					var docExists bool
					if err := existingDoc.Err(); err != nil {
						var httpErr *chttp.HTTPError
						if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusNotFound {
							log.Trace().
								Str("package_id", change.ID).
								Msg("Document not found in existing DB")
							docExists = false
						} else {
							log.Panic().Err(err).Msg("failed to get existing document")
						}
					} else {
						docExists = true
					}

					if change.Deleted {
						if docExists {
							existingRev, err := existingDoc.Rev()
							if err != nil {
								log.Panic().Err(err).Msg("failed to get existing revision")
							}

							// background context to make sure all of these are done before actually allowing the goroutine to exit
							localRev, err := db.Delete(context.Background(), change.ID, existingRev)
							if err != nil {
								log.Panic().Err(err).Msg("Could not delete replicator document")
							}

							log.Debug().
								Str("local_rev", localRev).
								Msg("deleted doc in CouchDB")
						} else {
							log.Trace().Msg("not deleting doc, because it doesn't exist")
						}
					} else {
						if err := existingDoc.ScanDoc(&pkg); err != nil {
							var httpErr *chttp.HTTPError
							if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusNotFound {
								// it's fine
							} else {
								log.Panic().Err(err).Msg("failed to scan document")
							}
						} else {
							log.Trace().Str("old_upstream_rev", pkg.Replicator.UpstreamRev).Msg("Preexisting document found")
						}

						pkg.Replicator.UpstreamRev = upstreamRev

						// background context to make sure all of these are done before actually allowing the goroutine to exit
						localRev, err := db.Put(context.Background(), change.ID, pkg)
						if err != nil {
							log.Panic().Err(err).Msg("Could not update replicator document")
						}

						log.Debug().
							Str("local_rev", localRev).
							Msg("updated doc in CouchDB")
					}

					tracker.NewLastSeenSeq(change.Seq)
					localLastSyncedSequenceID.Set(float64(change.Seq))
				}
			}
		}
	})

	wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("recovered", r).Msg("recovered from panic")
			}
		}()

		ticker := time.Tick(*fBatchedMetadataUpdateInterval)
		log.Info().
			Dur("interval", *fBatchedMetadataUpdateInterval).
			Int("batch_size", *fMetadataUpdateBatchSize).
			Msg("Starting metadata updater")

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker:
				statusView := db.Query(
					ctx,
					"_design/npm_replication",
					"status",
					kivik.Param("limit", strconv.Itoa(*fMetadataUpdateBatchSize)),
					kivik.Param("include_docs", "true"),
					kivik.Param("key", "out-of-date"),
				)
				if err := statusView.Err(); err != nil {
					log.Error().Err(err).Msg("could not fetch replicator statuses")
					continue
				}

				var fetcherWg sync.WaitGroup
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

					fetcherWg.Go(func() {
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
									return
								}

								log.Debug().
									Str("new_rev", newRev).
									Msg("refreshed metadata for package")
							}

							return
						} else {
							var version npm.Version

							if latestTag, ok := metadata.DistTags["latest"]; !ok {
								log.Warn().Interface("dist_tags", metadata.DistTags).Msg("Package has no latest tag")
							} else {
								latestVersion, ok := metadata.Versions[latestTag]
								if !ok {
									log.Warn().Str("latest_tag", latestTag).Msg("Latest tag is not a valid version")
									return
								}

								version = latestVersion
							}

							// generate new package document so that we don't have stale information in there
							pkg = RegistryPackage{
								Version: version,
								Rev_:    pkg.Rev_,
								Replicator: ReplicatorMetadata{
									UpstreamRev: pkg.Replicator.UpstreamRev,
									// mark the metadata as updated
									MetadataRev: &pkg.Replicator.UpstreamRev,
								},
							}

							// background context to make sure all of these are done before actually allowing the goroutine to exit
							newRev, err := db.Put(context.Background(), packageID, pkg)
							if err != nil {
								log.Error().Err(err).Msg("could not update replicator document")
								return
							}

							log.Debug().
								Str("new_rev", newRev).
								Msg("refreshed metadata for package")
						}
					})

					log.Trace().Msg("Metadata fetcher goroutine started")
				}
				fetcherWg.Wait()

				if statusView.Err() != nil {
					log.Panic().Err(statusView.Err()).Msg("failed to fetch replicator document statuses")
				}
			}
		}
	})

	if *fMetricsListenAddr != "" {
		wg.Go(func() {
			log.Info().Str("listen_address", *fMetricsListenAddr).Msg("Starting HTTP server...")
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("Failed to start HTTP server")
			}

			log.Info().Msg("HTTP server stopped")
		})
	}

	<-ctx.Done()
	cancel()

	httpServer.Shutdown(context.Background())

	wg.Wait()
}

var _upstreamSequenceID int64 = 0

func updateStats(ctx context.Context, npmClient *npm.Client, db *kivik.DB) {
	info, err := npmClient.Info(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to get registry info")
		return
	}

	upstreamDocumentCount.Set(float64(info.DocCount))
	upstreamSequenceID.Set(float64(info.UpdateSeq))
	_upstreamSequenceID = info.UpdateSeq

	stats, err := db.Stats(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to get db stats")
		return
	}

	localDBSize.Set(float64(stats.DiskSize))
	localDBActiveSize.Set(float64(stats.ActiveSize))
	localDocumentCount.With(prometheus.Labels{"status": "total"}).Set(float64(stats.DocCount))

	{
		statusView := db.Query(ctx, "_design/npm_replication", "status_count", kivik.Param("group", "true"))
		if err := statusView.Err(); err != nil {
			log.Error().Err(err).Msg("could not fetch replicator status count")
			return
		}
		outOfDate := 0
		upToDate := 0

		for statusView.Next() {
			var key string
			if err := statusView.ScanKey(&key); err != nil {
				log.Error().Err(err).Msg("failed to read replicator stat key")
				return
			}

			var output *int
			if key == "out-of-date" {
				output = &outOfDate
			} else if key == "up-to-date" {
				output = &upToDate
			} else {
				log.Error().Str("key", key).Msg("unknown key")
				return
			}

			if err := statusView.ScanValue(output); err != nil {
				log.Error().Err(err).Msg("failed to read replicator stat value")
				return
			}
		}
		if statusView.Err() != nil {
			log.Error().Err(statusView.Err()).Msg("failed to fetch replicator document stats")
			return
		}

		localDocumentCount.With(prometheus.Labels{"status": "out-of-date"}).Set(float64(outOfDate))
		localDocumentCount.With(prometheus.Labels{"status": "up-to-date"}).Set(float64(upToDate))
	}
}

type ReplicatorMetadata struct {
	UpstreamRev          string     `json:"upstreamRev"`
	MetadataRev          *string    `json:"metadataRev"`
	DownloadsLastUpdated *time.Time `json:"downloadsLastUpdated"`

	FoundInChangestreamButNotInRegistry bool `json:"foundInChangestreamButNotInRegistry"`
}

type RegistryPackage struct {
	npm.Version

	Rev_ *string `json:"_rev,omitempty"`

	Replicator ReplicatorMetadata `json:"replicator"`
}
