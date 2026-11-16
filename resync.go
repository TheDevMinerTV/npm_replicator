package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kivik/kivik/v4"
	"github.com/go-kivik/kivik/v4/couchdb/chttp"
	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
	"github.com/thedevminertv/npm-replicator/pkg/npm"
	"github.com/thedevminertv/npm-replicator/pkg/replicator"
	"github.com/thedevminertv/npm-replicator/pkg/retry"
)

// The resync reconciles the local database against the full upstream listing.
//
// The changes feed alone can't keep a mirror complete: npm drops a package
// from it without a deletion record when the package is made private, so the
// local copy lingers forever, and anything private while the changestream
// passed it never arrives at all. Walking upstream _all_docs page by page and
// comparing each ID range with the same range locally catches both, and only
// writes documents that actually differ, so metadata is refetched for those
// alone.

const (
	// The listing has to cover nearly every upstream document before an empty
	// page is taken as its end: a truncated listing would otherwise make the
	// whole remaining local range look dropped.
	resyncMinListedShare = 0.99

	// A range with more drop candidates than this is distrusted as a whole.
	// Real drops are a few hundred a day across millions of documents.
	resyncMinCandidateGuard = 100
	resyncMaxCandidateShare = 0.1

	resyncRegistryConcurrency = 4
	resyncFailureBackoff      = 10 * time.Minute
	resyncConflictAttempts    = 3
)

type resyncLocalRow struct {
	UpstreamRev string
	Dropped     bool
}

type resyncPlan struct {
	// Upsert lists upstream documents that are missing locally, behind, or
	// still marked as dropped.
	Upsert []npm.DocRow
	// Candidates lists local documents that upstream no longer has and that
	// aren't marked as dropped yet, with the upstream rev they were seen at.
	// They still need the registry to confirm.
	Candidates []npm.DocRow
}

func planResyncRange(upstream []npm.DocRow, local map[string]resyncLocalRow) resyncPlan {
	var plan resyncPlan

	inUpstream := make(map[string]struct{}, len(upstream))
	for _, row := range upstream {
		inUpstream[row.ID] = struct{}{}

		l, ok := local[row.ID]
		if !ok || l.Dropped || revIsNewer(row.Rev, l.UpstreamRev) {
			plan.Upsert = append(plan.Upsert, row)
		}
	}

	for id, l := range local {
		if _, ok := inUpstream[id]; !ok && !l.Dropped {
			plan.Candidates = append(plan.Candidates, npm.DocRow{ID: id, Rev: l.UpstreamRev})
		}
	}
	slices.SortFunc(plan.Candidates, func(a, b npm.DocRow) int { return strings.Compare(a.ID, b.ID) })

	return plan
}

// revIsNewer reports whether upstream should replace the local upstream rev.
// The changestream may already have applied a later change than the listing
// saw, and going back to the older rev would refetch metadata for nothing.
func revIsNewer(upstream, local string) bool {
	if upstream == local {
		return false
	}
	if local == "" {
		return true
	}

	upstreamGen, upstreamOK := revGeneration(upstream)
	localGen, localOK := revGeneration(local)
	if !upstreamOK || !localOK {
		return true
	}

	// same generation with another hash means upstream settled on a different
	// winner, which only it can know about
	return upstreamGen >= localGen
}

func revGeneration(rev string) (int64, bool) {
	gen, _, ok := strings.Cut(rev, "-")
	if !ok {
		return 0, false
	}

	n, err := strconv.ParseInt(gen, 10, 64)
	return n, err == nil
}

// tooManyCandidates distrusts a range in which a large share of local
// documents vanished upstream, which points at a broken listing rather than at
// that many packages going private at once.
func tooManyCandidates(candidates, local int) bool {
	return candidates > max(resyncMinCandidateGuard, int(float64(local)*resyncMaxCandidateShare))
}

// maybeResync starts or resumes a resync when one is due and the changestream
// has caught up, so that the listing isn't compared against a database that is
// still far behind it.
func maybeResync(ctx context.Context, npmClient *npm.Client, db *kivik.DB, tracker *Tracker, lastFailure *time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("recovered", r).Msg("recovered from panic in resync")
		}
	}()

	cursor, _, lastCompletedAt := tracker.GetResyncState()
	due := cursor != "" || lastCompletedAt == nil || time.Since(*lastCompletedAt) >= *fResyncInterval
	if !due || time.Since(*lastFailure) < resyncFailureBackoff {
		return
	}

	// without the upstream sequence there's no telling how far behind we are
	upstreamSeq := _upstreamSequenceID.Load()
	if upstreamSeq == 0 {
		return
	}
	if lag := upstreamSeq - tracker.LastSeq(); lag > 2*int64(*fChangestreamBatchSize) {
		log.Debug().Int64("lag", lag).Msg("resync due, waiting for the changestream to catch up")
		return
	}

	if err := runResync(ctx, npmClient, db, tracker); err != nil {
		if ctx.Err() != nil {
			return
		}

		*lastFailure = time.Now()
		log.Error().Err(err).Dur("backoff", resyncFailureBackoff).Msg("resync failed, it will resume later")
	}
}

func runResync(ctx context.Context, npmClient *npm.Client, db *kivik.DB, tracker *Tracker) error {
	start := time.Now()
	cursor, seen, _ := tracker.GetResyncState()

	info, err := npmClient.Info(ctx)
	if err != nil {
		return fmt.Errorf("could not fetch upstream info: %w", err)
	}

	log.Info().
		Str("cursor", cursor).
		Int64("listed", seen).
		Int64("upstream_documents", info.DocCount).
		Msg("Starting resync")

	counts := map[string]int{}
	count := func(action string, n int) {
		counts[action] += n
		resyncDocsTotal.WithLabelValues(action).Add(float64(n))
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		rows, full, err := fetchUpstreamPage(ctx, npmClient, cursor)
		if err != nil {
			return fmt.Errorf("could not list upstream after %q: %w", cursor, err)
		}

		for i, row := range rows {
			if (i == 0 && row.ID <= cursor) || (i > 0 && row.ID <= rows[i-1].ID) {
				log.Warn().Str("id", row.ID).Msg("upstream listing is out of order, the range may be misjudged")
			}
		}

		if !full && float64(seen+int64(len(rows))) < float64(info.DocCount)*resyncMinListedShare {
			return fmt.Errorf("upstream listing ended after %d of %d documents", seen+int64(len(rows)), info.DocCount)
		}

		// the last page owns everything after its predecessor, so that local
		// documents past the final upstream ID are compared as well
		endKey := ""
		if full {
			endKey = rows[len(rows)-1].ID
		}

		local, err := localResyncRange(ctx, db, cursor, endKey)
		if err != nil {
			return fmt.Errorf("could not read local range after %q: %w", cursor, err)
		}

		plan := planResyncRange(rows, local)

		for _, row := range plan.Upsert {
			action, err := resyncUpsert(ctx, db, row)
			if err != nil {
				return fmt.Errorf("could not reconcile %q: %w", row.ID, err)
			}
			if action != "" {
				count(action, 1)
			}
		}

		if tooManyCandidates(len(plan.Candidates), len(local)) {
			log.Warn().
				Str("after", cursor).
				Str("until", endKey).
				Int("candidates", len(plan.Candidates)).
				Int("local", len(local)).
				Msg("too many local documents missing upstream in one range, not marking any as dropped")
			count("drop_aborted", len(plan.Candidates))
		} else {
			dropCounts := resyncDropCandidates(ctx, npmClient, db, plan.Candidates)
			for action, n := range dropCounts {
				count(action, n)
			}

			// keep the cursor on this page if its checks were cut short, so
			// they're redone rather than put off until the next run. A single
			// package failing doesn't hold up the rest of the listing.
			if err := ctx.Err(); err != nil {
				return err
			}
			if failed := dropCounts["error"]; failed > 0 && failed*2 >= len(plan.Candidates) {
				return fmt.Errorf("registry checks failed for %d of %d candidates after %q", failed, len(plan.Candidates), cursor)
			}
		}

		seen += int64(len(rows))
		resyncPagesTotal.Inc()

		if !full {
			now := time.Now()
			if err := tracker.FinishResync(now); err != nil {
				log.Error().Err(err).Msg("failed to save resync completion")
			}
			resyncLastCompleted.Set(float64(now.Unix()))
			resyncLastDuration.Set(now.Sub(start).Seconds())

			log.Info().
				Int64("listed", seen).
				Dur("duration", now.Sub(start)).
				Interface("actions", counts).
				Msg("Finished resync")
			return nil
		}

		cursor = endKey
		if err := tracker.SetResyncProgress(cursor, seen); err != nil {
			log.Error().Err(err).Msg("failed to save resync progress")
		}
	}
}

// fetchUpstreamPage lists the upstream IDs after cursor. full reports whether
// upstream had a whole page to give, i.e. whether there may be more.
func fetchUpstreamPage(ctx context.Context, npmClient *npm.Client, cursor string) (rows []npm.DocRow, full bool, err error) {
	logger := log.With().Str("cursor", cursor).Logger()

	err = retry.ExponentialBackoff(ctx, 4, 5*time.Second, func() error {
		rows, err = npmClient.AllDocs(ctx, cursor, npm.AllDocsPageLimit)

		var httpErr httpclient.UnexpectedHTTPStatusCodeError
		if errors.As(err, &httpErr) && httpErr.StatusCode < 500 {
			return &retry.NonRetryableError{Err: err}
		}
		return err
	}, logger, nil)
	if err != nil {
		return nil, false, err
	}

	full = len(rows) == npm.AllDocsPageLimit
	// startkey is inclusive, and the cursor was already reconciled
	if cursor != "" && len(rows) > 0 && rows[0].ID == cursor {
		rows = rows[1:]
	}

	return rows, full, nil
}

// localResyncRange reads the local IDs in (after, until], or after everything
// past after if until is empty. The view collates raw, like upstream does.
func localResyncRange(ctx context.Context, db *kivik.DB, after, until string) (map[string]resyncLocalRow, error) {
	params := map[string]any{}
	if after != "" {
		params["startkey"] = after
	}
	if until != "" {
		params["endkey"] = until
	}

	view := db.Query(ctx, "_design/resync", "by_id", kivik.Params(params))
	defer view.Close()

	local := map[string]resyncLocalRow{}
	for view.Next() {
		id, err := view.ID()
		if err != nil {
			return nil, err
		}
		if id == after {
			continue
		}

		var value []any
		if err := view.ScanValue(&value); err != nil {
			return nil, err
		}

		var row resyncLocalRow
		if len(value) == 2 {
			row.UpstreamRev, _ = value[0].(string)
			row.Dropped, _ = value[1].(bool)
		}
		local[id] = row
	}

	return local, view.Err()
}

// resyncUpsert brings one local document in line with its upstream listing,
// retrying when another writer got to it first. The returned action is empty
// if nothing was written.
func resyncUpsert(ctx context.Context, db *kivik.DB, row npm.DocRow) (string, error) {
	for range resyncConflictAttempts - 1 {
		action, err := resyncUpsertOnce(ctx, db, row)
		if action != "conflict" {
			return action, err
		}
	}

	return resyncUpsertOnce(ctx, db, row)
}

// resyncUpsertOnce rereads the document, since the changestream may have moved
// it on since the range was read.
func resyncUpsertOnce(ctx context.Context, db *kivik.DB, row npm.DocRow) (string, error) {
	var pkg replicator.RegistryPackage

	exists := true
	if err := db.Get(ctx, row.ID).ScanDoc(&pkg); err != nil {
		var httpErr *chttp.HTTPError
		if !errors.As(err, &httpErr) || httpErr.HTTPStatus() != http.StatusNotFound {
			return "", err
		}
		exists = false
	}

	wasDropped := pkg.Replicator.DroppedFromFeedAt != nil
	newer := revIsNewer(row.Rev, pkg.Replicator.UpstreamRev)
	if exists && !wasDropped && !newer {
		return "", nil
	}

	action := "updated"
	switch {
	case !exists:
		action = "created"
	case wasDropped:
		action = "restored"
	}

	// a changed upstream rev is all it takes for the metadata updater to
	// refetch this package, and nothing else
	if newer {
		pkg.Replicator.UpstreamRev = row.Rev
	}
	pkg.Replicator.DroppedFromFeedAt = nil

	// background context to make sure the write isn't torn by shutdown
	if _, err := db.Put(context.Background(), row.ID, pkg); err != nil {
		var httpErr *chttp.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusConflict {
			return "conflict", nil
		}
		return "", err
	}

	log.Debug().Str("package_id", row.ID).Str("upstream_rev", row.Rev).Str("action", action).Msg("resync reconciled document")
	return action, nil
}

// resyncDropCandidates marks the candidates the registry no longer serves as
// dropped. Those it still serves are missing from the listing alone, which
// happens, and are left as they are.
func resyncDropCandidates(ctx context.Context, npmClient *npm.Client, db *kivik.DB, candidates []npm.DocRow) map[string]int {
	var (
		m      sync.Mutex
		counts = map[string]int{}
		wg     sync.WaitGroup
		sem    = make(chan struct{}, resyncRegistryConcurrency)
	)

	for _, candidate := range candidates {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("recovered", r).Str("package_id", candidate.ID).Msg("recovered from panic in resync drop check")
				}
			}()

			action := resyncDropCandidate(ctx, npmClient, db, candidate)
			if action == "" {
				return
			}

			m.Lock()
			counts[action]++
			m.Unlock()
		})
	}
	wg.Wait()

	return counts
}

func resyncDropCandidate(ctx context.Context, npmClient *npm.Client, db *kivik.DB, candidate npm.DocRow) string {
	id := candidate.ID
	log := log.With().Str("package_id", id).Logger()

	exists, err := npmClient.PackageExists(ctx, id)
	if err != nil {
		log.Warn().Err(err).Msg("could not check whether a package missing upstream still exists")
		return "error"
	}
	if exists {
		log.Debug().Msg("package missing upstream is still served by the registry")
		return "flaky"
	}

	var pkg replicator.RegistryPackage
	if err := db.Get(ctx, id).ScanDoc(&pkg); err != nil {
		var httpErr *chttp.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusNotFound {
			return ""
		}
		log.Error().Err(err).Msg("could not read document to mark as dropped")
		return "error"
	}
	// a different rev means it was published again since the range was read,
	// possibly after the registry answered
	if pkg.Replicator.DroppedFromFeedAt != nil || pkg.Replicator.UpstreamRev != candidate.Rev {
		return ""
	}

	now := time.Now()
	pkg.Replicator.DroppedFromFeedAt = &now

	// background context to make sure the write isn't torn by shutdown
	if _, err := db.Put(context.Background(), id, pkg); err != nil {
		var httpErr *chttp.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPStatus() == http.StatusConflict {
			return "conflict"
		}
		log.Error().Err(err).Msg("could not mark document as dropped")
		return "error"
	}

	log.Info().Msg("package was dropped from the feed, marked it")
	return "dropped"
}
