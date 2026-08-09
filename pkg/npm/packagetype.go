package npm

import "encoding/json"

// PackageType is a coarse classification of how a package expects to be
// consumed, derived from the latest version's "type", "module" and "exports"
// fields. It is deliberately rough: the registry metadata alone cannot tell us
// what is actually inside the tarball.
type PackageType string

const (
	// PackageTypeUnknown means there was no version to look at (no valid
	// "latest" tag, deleted package, ...).
	PackageTypeUnknown PackageType = "unknown"
	// PackageTypeESM means "type": "module" with no CommonJS entrypoint
	// advertised through exports.
	PackageTypeESM PackageType = "esm"
	// PackageTypeDual means the package ships both, either ESM-first
	// ("type": "module" plus a "require" condition) or CommonJS-first
	// (an "import" condition or the legacy "module" field).
	PackageTypeDual PackageType = "dual"
	// PackageTypeCJS means nothing advertises ESM.
	PackageTypeCJS PackageType = "cjs"
)

// maxExportsDepth bounds the exports walk. Real packages nest a handful of
// levels; the limit only exists so a pathological document cannot blow the
// stack.
const maxExportsDepth = 32

// DetectPackageType classifies v. See PackageType for what the results mean.
func (v Version) DetectPackageType() PackageType {
	if v.Version == "" {
		return PackageTypeUnknown
	}

	hasImport, hasRequire := v.exportsConditions()

	if v.Type == "module" {
		if hasRequire {
			return PackageTypeDual
		}

		return PackageTypeESM
	}

	if hasImport || v.Module != "" {
		return PackageTypeDual
	}

	return PackageTypeCJS
}

// exportsConditions reports whether the exports tree mentions an ESM and/or a
// CommonJS condition anywhere. It walks targets only, never the subpath keys
// they hang off, so a package exporting "./require" is not mistaken for a
// CommonJS entrypoint.
func (v Version) exportsConditions() (hasImport, hasRequire bool) {
	for _, target := range v.Exports {
		var node any
		if err := json.Unmarshal(target, &node); err != nil {
			continue
		}

		walkExports(node, 0, &hasImport, &hasRequire)

		if hasImport && hasRequire {
			break
		}
	}

	return hasImport, hasRequire
}

func walkExports(node any, depth int, hasImport, hasRequire *bool) {
	if depth > maxExportsDepth || (*hasImport && *hasRequire) {
		return
	}

	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			switch key {
			case "import", "module":
				*hasImport = true
			case "require":
				*hasRequire = true
			}

			walkExports(value, depth+1, hasImport, hasRequire)
		}

	case []any:
		for _, value := range n {
			walkExports(value, depth+1, hasImport, hasRequire)
		}
	}
}
