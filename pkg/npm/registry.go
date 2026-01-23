package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

type PackageMetadata struct {
	Name         string             `json:"name"`
	Keywords     []string           `json:"keywords,omitempty"`
	Repository   *Repository        `json:"repository,omitempty"`
	Author       *User              `json:"author,omitempty"`
	Maintainers  Users              `json:"maintainers,omitempty"`
	Contributors Users              `json:"contributors,omitempty"`
	DistTags     map[string]string  `json:"dist-tags"`
	Versions     map[string]Version `json:"versions"`
	Time         map[string]string  `json:"time"`
}

func (c *Client) PackageMetadata(ctx context.Context, name string) (*PackageMetadata, error) {
	escapedPackageName := url.PathEscape(name)

	body, err := c.registryClient.GetJSON(ctx, fmt.Sprintf("/%s", escapedPackageName), nil, nil, httpclient.ExactStatusCode(200))
	if err != nil {
		return nil, err
	}

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

	{
		// try decoding as Repository array
		var t []Repository
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			if len(t) > 0 {
				r.Type = t[0].Type
				r.URL = t[0].URL
			}

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

type User struct {
	Name  string  `json:"name"`
	Email *string `json:"email,omitempty"`
}

func (u *User) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			u.Name = t
			u.Email = nil
			return nil
		}
	}

	var t struct {
		Name  string  `json:"name"`
		Email *string `json:"email,omitempty"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	u.Name = t.Name
	u.Email = t.Email

	return nil
}

type Users []User

func (u Users) Len() int { return len(u) }

func (u *Users) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*u = Users{User{Name: t}}
			return nil
		}
	}

	var t []User
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	*u = t

	return nil
}

type Version struct {
	Name         string      `json:"name"`
	Keywords     []string    `json:"keywords,omitempty"`
	Repository   *Repository `json:"repository,omitempty"`
	Version      string      `json:"version"`
	Author       *User       `json:"author,omitempty"`
	Maintainers  []User      `json:"maintainers,omitempty"`
	Contributors []User      `json:"contributors,omitempty"`
	Dist         struct {
		Tarball      string `json:"tarball"`
		FileCount    *int   `json:"fileCount,omitempty"`
		UnpackedSize *int   `json:"unpackedSize,omitempty"`
	} `json:"dist"`
	Dependencies    map[string]string `json:"dependencies,omitempty"`
	DevDependencies map[string]string `json:"devDependencies,omitempty"`
	Engines         Engines           `json:"engines,omitempty"`
}

type Engines map[string]string

func (e *Engines) UnmarshalJSON(data []byte) error {
	{
		// try decoding as { name: string; version: string }[]
		var t []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*e = make(map[string]string, len(t))
			for _, v := range t {
				(*e)[v.Name] = v.Version
			}

			return nil
		}
	}

	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*e = make(map[string]string, 1)
			(*e)[t] = "format unsupported"

			return nil
		}
	}

	{
		// try decoding as string[]
		var t []string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*e = make(map[string]string, len(t))
			for _, v := range t {
				(*e)[v] = "format unsupported"
			}

			return nil
		}
	}

	var t map[string]string
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}

	*e = t

	return nil
}
