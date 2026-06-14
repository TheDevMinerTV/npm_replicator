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
	"strings"
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
	"github.com/thedevminertv/npm-replicator/internal"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
	"github.com/thedevminertv/npm-replicator/pkg/npm"
	"github.com/thedevminertv/npm-replicator/pkg/replicator"
	"github.com/thedevminertv/npm-replicator/pkg/webhooks"
)

type stringSlice []string

func (s *stringSlice) String() string         { return "" }
func (s *stringSlice) Set(value string) error { *s = append(*s, value); return nil }

var (
	localDBCompactionRunning = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "couchdb",
			Name:      "compaction_running",
			Help:      "Compaction process running according to CouchDB",
		},
	)
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

	metadataProxyErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "metadata",
			Name:      "proxy_errors_total",
			Help:      "Total number of proxy transport errors",
		},
		[]string{"proxy"},
	)

	downloadsUpdatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "updated_total",
			Help:      "Total number of packages whose download counts were updated",
		},
	)
	downloadsErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "errors_total",
			Help:      "Total number of download fetch errors",
		},
		[]string{"reason"},
	)
	downloadsFetchDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "fetch_duration_seconds",
			Help:      "Duration of download count fetches",
		},
	)
	downloadsDocumentCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "document_count",
			Help:      "Number of packages by download status",
		},
		[]string{"status"},
	)
	downloadsCountSum = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "count_sum",
			Help:      "Sum of download counts across all packages, per period",
		},
		[]string{"period"},
	)
	downloadsProxyErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "downloads",
			Name:      "proxy_errors_total",
			Help:      "Total number of proxy transport errors",
		},
		[]string{"proxy"},
	)

	tarballSizeUpdatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "tarball",
			Name:      "size_updated_total",
			Help:      "Total number of tarball sizes fetched and stored",
		},
	)
	tarballSizeErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "npm_replicator",
			Subsystem: "tarball",
			Name:      "size_errors_total",
			Help:      "Total number of tarball size fetch errors",
		},
		[]string{"reason"},
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

	fRegistryProxyFile     = flag.String("registry-proxy-file", "", "Path to file with proxy URLs (one per line) for the registry client")
	fRegistryProxyCooldown = flag.Duration("registry-proxy-cooldown", 30*time.Second, "Cooldown duration for downed proxies before retrying")

	fDownloadsEnabled         = flag.Bool("downloads-enabled", false, "Enable download count fetching")
	fDownloadsUpdateInterval  = flag.Duration("downloads-update-interval", 60*time.Second, "Interval between download count update batches")
	fDownloadsUpdateBatchSize = flag.Int("downloads-update-batch-size", 10, "Batch size for download count updates")
	fDownloadsStaleness       = flag.Duration("downloads-staleness", 7*24*time.Hour, "How old download counts can be before re-fetching")
	fDownloadsProxyFile       = flag.String("downloads-proxy-file", "", "Path to file with proxy URLs (one per line) for the downloads client")
	fDownloadsProxyCooldown   = flag.Duration("downloads-proxy-cooldown", 30*time.Second, "Cooldown duration for downed proxies before retrying")

	fWebhooksEnabled = flag.Bool("webhooks-enabled", false, "Enable webhook notifications for package updates")

	fWebhookAuthorizations stringSlice
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	flag.Var(&fWebhookAuthorizations, "webhook-authorization", "Webhook authorization in format url=token (can be repeated)")
	flag.Parse()

	authMap := make(map[string]string)
	for _, auth := range fWebhookAuthorizations {
		parts := strings.SplitN(auth, "=", 2)
		url := strings.TrimSpace(parts[0])
		token := strings.TrimSpace(parts[1])
		if url == "" || token == "" {
			log.Fatal().Str("auth", auth).Msg("Webhook authorization url and token cannot be empty")
		}
		if _, exists := authMap[url]; exists {
			log.Fatal().Str("url", url).Msg("Duplicate webhook authorization for URL")
		}
		authMap[url] = token
	}
	webhooks.WebhookAuthorizations = authMap

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
		localDBCompactionRunning,
		localDBActiveSize,
		localDBSize,
		localDocumentCount,
		localLastSyncedSequenceID,
		upstreamDocumentCount,
		upstreamSequenceID,
		metadataProxyErrors,
		webhooks.WebhookCallsTotal,
		webhooks.WebhookRetriesTotal,
		webhooks.WebhookEndpointRetriesTotal,
		webhooks.WebhookSubscribedPackages,
		downloadsUpdatedTotal,
		downloadsErrorsTotal,
		downloadsFetchDuration,
		downloadsDocumentCount,
		downloadsCountSum,
		downloadsProxyErrors,
		tarballSizeUpdatedTotal,
		tarballSizeErrorsTotal,
	)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	var npmOpts []npm.ClientOpt
	if *fRegistryProxyFile != "" {
		proxy, err := internal.LoadProxyList(*fRegistryProxyFile, *fRegistryProxyCooldown, metadataProxyErrors)
		if err != nil {
			log.Fatal().Err(err).Str("file", *fRegistryProxyFile).Msg("Failed to load registry proxy list")
		}

		npmOpts = append(npmOpts, npm.WithRegistryHTTPClient(&http.Client{
			Transport: proxy,
			Timeout:   10 * time.Second,
		}))
	}
	if *fDownloadsProxyFile != "" {
		proxy, err := internal.LoadProxyList(*fDownloadsProxyFile, *fDownloadsProxyCooldown, downloadsProxyErrors)
		if err != nil {
			log.Fatal().Err(err).Str("file", *fDownloadsProxyFile).Msg("Failed to load proxy list")
		}

		npmOpts = append(npmOpts, npm.WithDownloadsHTTPClient(&http.Client{
			Transport: proxy,
			Timeout:   10 * time.Second,
		}))
	}
	npmClient := npm.New(npmOpts...)

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

	if *fWebhooksEnabled {
		webhooks.RefreshWebhookListeners(ctx)
		wg.Go(func() {
			ticker := time.Tick(1 * time.Hour)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker:
					webhooks.RefreshWebhookListeners(ctx)
				}
			}
		})
	}

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

					var pkg replicator.RegistryPackage

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

						if *fWebhooksEnabled {
							payload, err := json.Marshal(webhooks.NewChangestreamUpdatedData(pkg))
							if err != nil {
								log.Error().Err(err).Str("package", change.ID).Msg("Failed to marshal webhook payload")
							} else {
								webhooks.CallWebhooksAsync(ctx, change.ID, payload)
							}
						}
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

					var pkg replicator.RegistryPackage
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

							var httpErr httpclient.UnexpectedHTTPStatusCodeError
							if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
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

							var jsonErr *json.UnmarshalTypeError
							if errors.As(err, &jsonErr) {
								log.Error().Err(err).Msg("Could not unmarshal JSON response from upstream registry")

								pkg.Replicator.HasJSONParseError = true

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
							hasInvalidTag := false

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
							pkg = replicator.RegistryPackage{
								Version:   version,
								Rev_:      pkg.Rev_,
								Downloads: pkg.Downloads,
								Replicator: replicator.ReplicatorMetadata{
									UpstreamRev:          pkg.Replicator.UpstreamRev,
									MetadataRev:          &pkg.Replicator.UpstreamRev,
									DownloadsLastUpdated: pkg.Replicator.DownloadsLastUpdated,
									HasInvalidTag:        hasInvalidTag,
								},
							}

							if pkg.Dist.Tarball != "" {
								size, err := npmClient.TarballSize(ctx, pkg.Dist.Tarball)
								if err != nil {
									reason := "fetch"
									var httpErr httpclient.UnexpectedHTTPStatusCodeError
									if errors.As(err, &httpErr) {
										reason = strconv.Itoa(httpErr.StatusCode)
									}
									tarballSizeErrorsTotal.With(prometheus.Labels{"reason": reason}).Inc()
									log.Warn().Err(err).Str("tarball", pkg.Dist.Tarball).Msg("failed to fetch tarball size")
								} else {
									pkg.TarballSize = &size
									tarballSizeUpdatedTotal.Inc()
								}
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

							if *fWebhooksEnabled {
								payload, err := json.Marshal(webhooks.NewMetadataUpdatedData(pkg))
								if err != nil {
									log.Error().Err(err).Str("package", packageID).Msg("Failed to marshal webhook payload")
								} else {
									webhooks.CallWebhooksAsync(ctx, packageID, payload)
								}
							}
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

	if *fDownloadsEnabled {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("recovered", r).Msg("recovered from panic in downloads updater")
				}
			}()

			ticker := time.Tick(*fDownloadsUpdateInterval)
			log.Info().
				Dur("interval", *fDownloadsUpdateInterval).
				Int("batch_size", *fDownloadsUpdateBatchSize).
				Dur("staleness", *fDownloadsStaleness).
				Msg("Starting downloads updater")

			for {
				select {
				case <-ctx.Done():
					return

				case <-ticker:
					threshold := time.Now().Add(-*fDownloadsStaleness).Format(time.RFC3339)

					staleView := db.Query(
						ctx,
						"_design/npm_replication",
						"downloads_stale",
						kivik.Param("limit", strconv.Itoa(*fDownloadsUpdateBatchSize)),
						kivik.Param("include_docs", "true"),
						kivik.Param("startkey", nil),
						kivik.Param("endkey", threshold),
					)
					if err := staleView.Err(); err != nil {
						log.Error().Err(err).Msg("could not fetch stale downloads view")
						continue
					}

					var dlWg sync.WaitGroup
					for staleView.Next() {
						var packageID string
						if err := staleView.ScanValue(&packageID); err != nil {
							// value is null from the view, get ID instead
						}
						packageID, _ = staleView.ID()

						var pkg replicator.RegistryPackage
						if err := staleView.ScanDoc(&pkg); err != nil {
							log.Error().Err(err).Str("package", packageID).Msg("failed to scan document for downloads update")
							continue
						}

						log := log.With().Str("package_id", packageID).Logger()

						dlWg.Go(func() {
							start := time.Now()

							counts, err := npmClient.PackageAllDownloads(ctx, packageID, pkg.Version.Version)
							duration := time.Since(start)
							downloadsFetchDuration.Observe(duration.Seconds())

							if err != nil {
								var httpErr httpclient.UnexpectedHTTPStatusCodeError
								if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
									log.Debug().Msg("Package not found on downloads API, setting zero counts")
									counts = &npm.DownloadCounts{}
								} else {
									log.Error().Err(err).Msg("failed to fetch download counts")
									downloadsErrorsTotal.With(prometheus.Labels{"reason": "fetch"}).Inc()
									return
								}
							}

							now := time.Now()
							pkg.Downloads = counts
							pkg.Replicator.DownloadsLastUpdated = &now

							_, err = db.Put(context.Background(), packageID, pkg)
							if err != nil {
								var httpErr *chttp.HTTPError
								if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusConflict {
									log.Debug().Msg("CouchDB conflict updating downloads, will retry next cycle")
									downloadsErrorsTotal.With(prometheus.Labels{"reason": "conflict"}).Inc()
								} else {
									log.Error().Err(err).Msg("could not update downloads in CouchDB")
									downloadsErrorsTotal.With(prometheus.Labels{"reason": "put"}).Inc()
								}
								return
							}

							downloadsUpdatedTotal.Inc()
							log.Debug().
								Int("last_day", counts.LastDay).
								Int("last_week", counts.LastWeek).
								Int("last_month", counts.LastMonth).
								Msg("updated download counts")
						})
					}
					dlWg.Wait()

					if staleView.Err() != nil {
						log.Error().Err(staleView.Err()).Msg("error iterating stale downloads view")
					}
				}
			}
		})
	}

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

	compactRunning := float64(0)
	if stats.CompactRunning {
		compactRunning = 1
	}
	localDBCompactionRunning.Set(compactRunning)

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
		faulty := 0
		deleted := 0
		isFixableWithTime := 0

		for statusView.Next() {
			var key string
			if err := statusView.ScanKey(&key); err != nil {
				log.Error().Err(err).Msg("failed to read replicator stat key")
				return
			}

			var output *int
			switch key {
			case "out-of-date":
				output = &outOfDate
			case "up-to-date":
				output = &upToDate
			case "faulty":
				output = &faulty
			case "deleted":
				output = &deleted
			case "is-fixable-with-time":
				output = &isFixableWithTime
			default:
				log.Warn().Str("key", key).Msg("unknown key")
				continue
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
		localDocumentCount.With(prometheus.Labels{"status": "faulty"}).Set(float64(faulty))
		localDocumentCount.With(prometheus.Labels{"status": "deleted"}).Set(float64(deleted))
		localDocumentCount.With(prometheus.Labels{"status": "is-fixable-with-time"}).Set(float64(isFixableWithTime))
	}

	if *fDownloadsEnabled {
		dlStatusView := db.Query(ctx, "_design/npm_replication", "downloads_status_count", kivik.Param("group", "true"))
		if err := dlStatusView.Err(); err != nil {
			log.Error().Err(err).Msg("could not fetch downloads status count")
			return
		}

		neverFetched := 0
		dlUpToDate := 0
		stale := 0

		for dlStatusView.Next() {
			var key string
			if err := dlStatusView.ScanKey(&key); err != nil {
				log.Error().Err(err).Msg("failed to read downloads stat key")
				return
			}

			var output *int
			switch key {
			case "never-fetched":
				output = &neverFetched
			case "up-to-date":
				output = &dlUpToDate
			case "stale":
				output = &stale
			default:
				log.Warn().Str("key", key).Msg("unknown downloads status key")
				continue
			}

			if err := dlStatusView.ScanValue(output); err != nil {
				log.Error().Err(err).Msg("failed to read downloads stat value")
				return
			}
		}
		if dlStatusView.Err() != nil {
			log.Error().Err(dlStatusView.Err()).Msg("failed to fetch downloads document stats")
			return
		}

		downloadsDocumentCount.With(prometheus.Labels{"status": "never-fetched"}).Set(float64(neverFetched))
		downloadsDocumentCount.With(prometheus.Labels{"status": "up-to-date"}).Set(float64(dlUpToDate))
		downloadsDocumentCount.With(prometheus.Labels{"status": "stale"}).Set(float64(stale))

		sumView := db.Query(ctx, "_design/downloads", "sum", kivik.Param("reduce", "true"))
		if err := sumView.Err(); err != nil {
			log.Error().Err(err).Msg("could not fetch downloads sum view")
			return
		}

		var sums [4]int64
		if sumView.Next() {
			if err := sumView.ScanValue(&sums); err != nil {
				log.Error().Err(err).Msg("failed to read downloads sum value")
				return
			}
		}
		if sumView.Err() != nil {
			log.Error().Err(sumView.Err()).Msg("failed to fetch downloads sum")
			return
		}

		downloadsCountSum.With(prometheus.Labels{"period": "last-day"}).Set(float64(sums[0]))
		downloadsCountSum.With(prometheus.Labels{"period": "last-week"}).Set(float64(sums[1]))
		downloadsCountSum.With(prometheus.Labels{"period": "last-month"}).Set(float64(sums[2]))
		downloadsCountSum.With(prometheus.Labels{"period": "last-week-version"}).Set(float64(sums[3]))
	}
}
