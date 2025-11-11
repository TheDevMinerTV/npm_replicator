package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

type PackageMetadata struct {
	Name        string       `json:"name"`
	Keywords    []string     `json:"keywords,omitempty"`
	Repository  *Repository  `json:"repository,omitempty"`
	Author      *Author      `json:"author,omitempty"`
	Maintainers []Maintainer `json:"maintainers,omitempty"`
}

func (c *Client) PackageMetadata(ctx context.Context, name string) (*PackageMetadata, error) {
	escapedPackageName := url.PathEscape(name)

	body, err := c.registryClient.GetJSON(ctx, fmt.Sprintf("/%s", escapedPackageName), nil, nil)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	metadata := &PackageMetadata{}
	if err := json.NewDecoder(body).Decode(metadata); err != nil {
		return nil, err
	}

	return metadata, nil
}

type Repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

func (r *Repository) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			r.Type = "Unknown"
			r.URL = t
			return nil
		}
	}

	var t struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	r.Type = t.Type
	r.URL = t.URL

	return nil
}

type Maintainer struct {
	Name  string  `json:"name"`
	Email *string `json:"email,omitempty"`
}

func (m *Maintainer) UnmarshalJSON(data []byte) error {
	var t struct {
		Name  string  `json:"name"`
		Email *string `json:"email,omitempty"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	m.Name = t.Name
	m.Email = t.Email

	return nil
}

type Author struct {
	Name  string  `json:"name"`
	Email *string `json:"email,omitempty"`
}

func (m *Author) UnmarshalJSON(data []byte) error {
	var t struct {
		Name  string  `json:"name"`
		Email *string `json:"email,omitempty"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	m.Name = t.Name
	m.Email = t.Email

	return nil
}
