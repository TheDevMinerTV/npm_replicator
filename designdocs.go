package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"reflect"

	"github.com/go-kivik/kivik/v4"
	"github.com/go-kivik/kivik/v4/couchdb/chttp"
	"github.com/rs/zerolog/log"
)

//go:embed designdocs/*.json
var designDocs embed.FS

// ensureDesignDocs writes every design document in designdocs/ that is
// missing or differs from what CouchDB holds. Unchanged ones are left alone,
// because any write rebuilds their indexes from scratch.
func ensureDesignDocs(ctx context.Context, db *kivik.DB) error {
	entries, err := designDocs.ReadDir("designdocs")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		raw, err := designDocs.ReadFile(path.Join("designdocs", entry.Name()))
		if err != nil {
			return err
		}

		var want map[string]any
		if err := json.Unmarshal(raw, &want); err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
		id, _ := want["_id"].(string)
		if id == "" {
			return fmt.Errorf("%s: missing _id", entry.Name())
		}

		log := log.With().Str("design_doc", id).Logger()

		var have map[string]any
		if err := db.Get(ctx, id).ScanDoc(&have); err != nil {
			var httpErr *chttp.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPStatus() != http.StatusNotFound {
				return fmt.Errorf("%s: %w", id, err)
			}
		}

		if have != nil {
			rev := have["_rev"]
			delete(have, "_rev")
			if reflect.DeepEqual(have, want) {
				log.Debug().Msg("design document is up to date")
				continue
			}
			want["_rev"] = rev
		}

		if _, err := db.Put(ctx, id, want); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		log.Info().Msg("updated design document, its views will reindex")
	}

	return nil
}
