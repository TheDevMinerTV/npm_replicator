package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/thedevminertv/npm-replicator/pkg/httpclient"
)

type PackageMetadata struct {
	Name         string               `json:"name"`
	Keywords     Keywords             `json:"keywords,omitempty"`
	Repository   *Repository          `json:"repository,omitempty"`
	Author       Users                `json:"author,omitempty"`
	Maintainers  Users                `json:"maintainers,omitempty"`
	Contributors Users                `json:"contributors,omitempty"`
	DistTags     map[string]string    `json:"dist-tags"`
	Versions     map[string]Version   `json:"versions"`
	Time         map[string]TimeEntry `json:"time"`
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

// TarballSize fetches the byte length of a tarball. npm tarballs sit behind
// Cloudflare, which strips Content-Length from HEAD responses, so we issue a
// single-byte range GET and parse Content-Range (format: "bytes 0-0/<total>").
func (c *Client) TarballSize(ctx context.Context, tarballURL string) (int64, error) {
	respHeader, _, err := c.tarballClient.Get(ctx, tarballURL, nil, http.Header{
		"Range": []string{"bytes=0-0"},
	}, httpclient.ExactStatusCode(http.StatusPartialContent))
	if err != nil {
		return 0, err
	}

	cr := respHeader.Get("Content-Range")
	if cr == "" {
		return 0, fmt.Errorf("missing Content-Range header")
	}

	slash := strings.LastIndex(cr, "/")
	if slash < 0 {
		return 0, fmt.Errorf("malformed Content-Range %q", cr)
	}

	return strconv.ParseInt(cr[slash+1:], 10, 64)
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
		// try decoding as single user
		var t User
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*u = Users{t}
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
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Keywords     Keywords         `json:"keywords,omitempty"`
	Repository   *Repository      `json:"repository,omitempty"`
	Version      string           `json:"version"`
	Author       Users            `json:"author,omitempty"`
	Maintainers  Users            `json:"maintainers,omitempty"`
	Contributors Users            `json:"contributors,omitempty"`
	Deprecated   *DeprecationInfo `json:"deprecated,omitempty"`
	Dist         struct {
		Tarball      string `json:"tarball"`
		FileCount    *int   `json:"fileCount,omitempty"`
		UnpackedSize *int   `json:"unpackedSize,omitempty"`
	} `json:"dist"`
	Dependencies    map[string]string `json:"dependencies,omitempty"`
	DevDependencies map[string]string `json:"devDependencies,omitempty"`
	Engines         Engines           `json:"engines,omitempty"`
	Bin             Bin               `json:"bin,omitempty"`

	// Module resolution fields, stored so consumers can re-derive their own
	// classification; DetectPackageType condenses them into a PackageType.
	// Normalize canonicalizes all four.
	Type    LooseString `json:"type,omitempty"`
	Main    Paths       `json:"main,omitempty"`
	Module  LooseString `json:"module,omitempty"`
	Exports Exports     `json:"exports,omitempty"`
}

// Paths is a package.json field documented as a single module path but
// published as an array often enough that keeping only one entry would lose
// real data. Every spelling decodes to a slice, so consumers never branch on
// shape.
type Paths []string

func (p *Paths) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			if t != "" {
				*p = Paths{t}
			}

			return nil
		}
	}

	{
		// try decoding as an array, keeping the entries that are strings; a
		// stray null or number in there should not cost us the rest
		var t []any
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			paths := make(Paths, 0, len(t))
			for _, entry := range t {
				if s, ok := entry.(string); ok && s != "" {
					paths = append(paths, s)
				}
			}

			if len(paths) > 0 {
				*p = paths
			}

			return nil
		}
	}

	// numbers, booleans, objects: nothing sensible to keep
	*p = nil

	return nil
}

// Normalize canonicalizes every entry and drops the ones that collapse onto
// each other, so "index.js" and "./index.js" do not both end up stored.
func (p *Paths) Normalize() {
	if len(*p) == 0 {
		return
	}

	normalized := make(Paths, 0, len(*p))
	seen := make(map[string]struct{}, len(*p))

	for _, path := range *p {
		path = normalizePath(path)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}

	if len(normalized) == 0 {
		*p = nil
		return
	}

	*p = normalized
}

// LooseString is a string field that tolerates the non-string values found in
// the wild ("main": ["./index.js"], "main": null, "main": 0). Anything that
// isn't usable decodes to the empty string instead of failing the whole
// packument, which would otherwise flag the package with hasJSONParseError.
type LooseString string

func (s *LooseString) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var t string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*s = LooseString(t)
			return nil
		}
	}

	{
		// try decoding as string array, keeping the first entry
		var t []string
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			if len(t) > 0 {
				*s = LooseString(t[0])
			}

			return nil
		}
	}

	// numbers, booleans, objects: nothing sensible to keep
	*s = ""

	return nil
}

// Normalize canonicalizes the version in place so that every document written
// to the database has the same shape no matter which of npm's many accepted
// spellings the publisher used. It is a no-op on a zero version, which is what
// a package without a usable "latest" tag decodes to.
func (v *Version) Normalize(pkgName string) {
	if v.Version == "" {
		return
	}

	// resolve a bare-string bin to its unscoped-name command
	v.Bin.Normalize(pkgName)

	v.Type = LooseString(normalizeModuleType(string(v.Type)))
	v.Module = LooseString(normalizePath(string(v.Module)))
	v.Main.Normalize()
	v.Exports.Normalize()
}

// normalizeModuleType lower-cases the "type" field and makes npm's default
// explicit, so a view never has to spell out (doc.type || "commonjs").
// Values other than module/commonjs are kept verbatim rather than coerced —
// they are a small but real signal that a package.json is broken.
func normalizeModuleType(moduleType string) string {
	moduleType = strings.ToLower(strings.TrimSpace(moduleType))
	if moduleType == "" {
		return "commonjs"
	}

	return moduleType
}

// normalizePath canonicalizes a module path to the relative "./x" form npm
// resolves it as. Windows-authored packages ship backslashes, and the leading
// "./" is optional in package.json but conventional everywhere else.
func normalizePath(path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if path == "" || path == "." || path == ".." {
		return path
	}

	// already relative, absolute, or a URL-ish value we should not touch
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") ||
		strings.HasPrefix(path, "/") || strings.Contains(path, "://") {
		return path
	}

	return "./" + path
}

// Bin represents the npm package "bin" field, normalized into a map of command -> script path
type Bin map[string]string

// binPendingName is the placeholder key holding a bare-string bin's path until
// Normalize rewrites it to the package's unscoped name.
const binPendingName = ""

func (b *Bin) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	// bare string: a single executable whose command name is the package's
	// unscoped name — not visible here, so defer it to Normalize.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*b = Bin{binPendingName: s}
		return nil
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	*b = m

	return nil
}

func (b Bin) Normalize(pkgName string) {
	path, ok := b[binPendingName]
	if !ok {
		return
	}

	delete(b, binPendingName)
	b[unscopedBinName(pkgName)] = path
}

func unscopedBinName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
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

type TimeEntry struct {
	Time     string   `json:"time,omitempty"`
	Versions []string `json:"versions,omitempty"`
}

func (t *TimeEntry) UnmarshalJSON(data []byte) error {
	// First try to unmarshal as string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Time = s
		return nil
	}

	// If not string, try as object
	var obj struct {
		Time     string   `json:"time"`
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	t.Time = obj.Time
	t.Versions = obj.Versions

	return nil
}

func (t *TimeEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time)
}

type Keywords []string

func (k Keywords) Len() int { return len(k) }

func (k *Keywords) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var w string
		if err := json.Unmarshal(data, &w); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			*k = Keywords{w}
			return nil
		}
	}

	var w []string
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}

	*k = w

	return nil
}

type DeprecationInfo struct {
	Deprecated bool    `json:"deprecated"`
	Message    *string `json:"message,omitempty"`
}

func (d *DeprecationInfo) UnmarshalJSON(data []byte) error {
	{
		// try decoding as string
		var msg string
		if err := json.Unmarshal(data, &msg); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			d.Deprecated = true
			d.Message = &msg

			return nil
		}
	}

	{
		// try as bool
		var deprecated bool
		if err := json.Unmarshal(data, &deprecated); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			d.Deprecated = deprecated

			return nil
		}
	}

	// try as the object itself
	var obj struct {
		Deprecated bool    `json:"deprecated"`
		Message    *string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	d.Deprecated = obj.Deprecated
	d.Message = obj.Message

	return nil
}
