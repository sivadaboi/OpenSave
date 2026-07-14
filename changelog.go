// Package opensave exposes repo-root assets that need to ship inside the
// binary. go:embed can't reach parent directories, so the canonical
// CHANGELOG.md living at the repo root (where GitHub expects it) is
// embedded from this root-level package.
package opensave

import _ "embed"

// Changelog is the full CHANGELOG.md contents, shown in the app's About
// dialog and after updates.
//
//go:embed CHANGELOG.md
var Changelog string
