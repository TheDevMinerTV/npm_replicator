package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

const DownloadsBaseURL = "https://api.npmjs.org"

type DownloadCounts struct {
	LastDay         int `json:"lastDay"`
	LastWeek        int `json:"lastWeek"`
	LastWeekVersion int `json:"lastWeekVersion"`
	LastMonth       int `json:"lastMonth"`
}

type DownloadPointResponse struct {
	Downloads int    `json:"downloads"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Package   string `json:"package"`
}

type VersionDownloadsResponse struct {
	Package   string         `json:"package"`
	Downloads map[string]int `json:"downloads"`
}

func (c *Client) PackageDownloads(ctx context.Context, name string, period string) (*DownloadPointResponse, error) {
	escapedName := url.PathEscape(name)
	path := fmt.Sprintf("/downloads/point/%s/%s", period, escapedName)

	body, err := c.downloadsClient.GetJSON(ctx, path, nil, nil, httpclient.AllSuccessful)
	if err != nil {
		return nil, err
	}

	var resp DownloadPointResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode downloads response: %w", err)
	}

	return &resp, nil
}

func (c *Client) PackageVersionDownloads(ctx context.Context, name string) (*VersionDownloadsResponse, error) {
	escapedName := url.PathEscape(name)
	path := fmt.Sprintf("/versions/%s/last-week", escapedName)

	body, err := c.downloadsClient.GetJSON(ctx, path, nil, nil, httpclient.AllSuccessful)
	if err != nil {
		return nil, err
	}

	var resp VersionDownloadsResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode version downloads response: %w", err)
	}

	return &resp, nil
}

func (c *Client) PackageAllDownloads(ctx context.Context, name string, version string) (*DownloadCounts, error) {
	lastDay, err := c.PackageDownloads(ctx, name, "last-day")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch last-day downloads: %w", err)
	}

	lastWeek, err := c.PackageDownloads(ctx, name, "last-week")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch last-week downloads: %w", err)
	}

	lastMonth, err := c.PackageDownloads(ctx, name, "last-month")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch last-month downloads: %w", err)
	}

	var lastWeekVersion int
	if version != "" {
		versionDls, err := c.PackageVersionDownloads(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch per-version downloads: %w", err)
		}
		lastWeekVersion = versionDls.Downloads[version]
	}

	return &DownloadCounts{
		LastDay:         lastDay.Downloads,
		LastWeek:        lastWeek.Downloads,
		LastWeekVersion: lastWeekVersion,
		LastMonth:       lastMonth.Downloads,
	}, nil
}
