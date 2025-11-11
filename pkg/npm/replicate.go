package npm

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/rs/zerolog/log"
)

type InfoResponse struct {
	DocCount  int64 `json:"doc_count"`
	UpdateSeq int64 `json:"update_seq"`
}

func (c *Client) Info(ctx context.Context) (*InfoResponse, error) {
	body, err := c.replicateClient.GetJSON(ctx, "/", nil, nil)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch registry info")
		return nil, err
	}
	defer body.Close()

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

	body, err := c.replicateClient.GetJSON(ctx, "/_changes", q, nil)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch registry changes")
		return nil, err
	}
	defer body.Close()

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
