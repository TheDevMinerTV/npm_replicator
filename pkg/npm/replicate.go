package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

type InfoResponse struct {
	DocCount  int64 `json:"doc_count"`
	UpdateSeq int64 `json:"update_seq"`
}

func (c *Client) Info(ctx context.Context) (*InfoResponse, error) {
	body, err := c.replicateClient.GetJSON(ctx, "/", nil, nil, httpclient.ExactStatusCode(200))
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch registry info")
		return nil, err
	}

	var response InfoResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		log.Error().Err(err).Msg("failed to parse registry info response")
		return nil, err
	}

	log.Info().Msg("Got registry info")

	return &response, nil
}

type Change struct {
	Rev string `json:"rev"`
}

type ChangeResult struct {
	Seq     int64    `json:"seq"`
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
	Deleted bool     `json:"deleted"`
}

type ChangesResponse struct {
	Results []ChangeResult `json:"results"`
	LastSeq int64          `json:"last_seq"`
}

func (c *Client) Changes(ctx context.Context, startSeq int64, limit int) (*ChangesResponse, error) {
	q := url.Values{}
	q.Set("since", strconv.FormatInt(startSeq, 10))
	q.Set("limit", strconv.Itoa(limit))

	log.Info().Int64("start_sequence_id", startSeq).Msg("Fetching changes for range")

	body, err := c.replicateClient.GetJSON(ctx, "/_changes", q, nil, httpclient.ExactStatusCode(200))
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch registry changes")
		return nil, err
	}

	var response ChangesResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		log.Error().Err(err).Msg("failed to parse registry changes response")
		return nil, err
	}

	log.Info().
		Int64("start_sequence_id", startSeq).
		Int64("end_sequence_id", response.LastSeq).
		Int("results", len(response.Results)).
		Msg("Got changes for range")

	return &response, nil
}

// AllDocsPageLimit is the largest page replicate.npmjs.com serves; anything
// above it is answered with 400.
const AllDocsPageLimit = 10000

type DocRow struct {
	ID  string
	Rev string
}

// AllDocs lists live upstream documents ordered by ID, starting at startKey
// (inclusive) or at the very first ID if it's empty. The endpoint rejects
// skip, so callers page by passing the last ID they saw and dropping it.
//
// Unlike the changes feed, which only shows what changed, this lists exactly
// what currently exists: packages that went private disappear from both
// without a deletion record, but only a listing reveals that they're gone.
func (c *Client) AllDocs(ctx context.Context, startKey string, limit int) ([]DocRow, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if startKey != "" {
		key, err := json.Marshal(startKey)
		if err != nil {
			return nil, err
		}
		q.Set("startkey", string(key))
	}

	body, err := c.replicateClient.GetJSON(ctx, "/_all_docs", q, nil, httpclient.ExactStatusCode(200))
	if err != nil {
		return nil, err
	}

	var response struct {
		Rows []struct {
			ID    string `json:"id"`
			Value struct {
				Rev string `json:"rev"`
			} `json:"value"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse all docs response: %w", err)
	}

	rows := make([]DocRow, len(response.Rows))
	for i, row := range response.Rows {
		rows[i] = DocRow{ID: row.ID, Rev: row.Value.Rev}
	}

	return rows, nil
}
