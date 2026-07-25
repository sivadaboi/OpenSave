package presets

import "strings"

// cacheDirNames are folders that are shader/GPU/engine caches or other
// regenerated data — never a save worth tracking, and syncing them would
// shuttle hundreds of megabytes of machine-specific junk between devices.
var cacheDirNames = map[string]bool{
	// Direct3D / graphics API caches (the DX12 noise reported by users).
	"dx12cache": true, "dxcache": true, "d3dcache": true, "d3dscache": true,
	"dxil": true, "dxbc": true, "pipelinecache": true, "psocache": true,
	// Engine + vendor shader caches.
	"shadercache": true, "shadercachedb": true, "shaders": true,
	"shadercompiler": true, "derivedatacache": true, "ddc": true,
	"gpucache": true, "glcache": true, "vulkancache": true, "nvidiacache": true,
	// Generic caches and regenerated state.
	"cache": true, "caches": true, "cacheddata": true, "temp": true, "tmp": true,
	"logs": true, "log": true, "crashes": true, "crashdumps": true,
	"crashreports": true, "webcache": true, "mediacache": true,
}

// cacheDirSuffixes catch the same families when a game prefixes them with its
// own name (e.g. "FortniteShaderCache", "AnvilDX12Cache", "DerivedDataCache").
// A trailing "cache" is a strong enough signal on its own; save folders are
// checked only after this, so an oddity like "SaveCache" is treated as cache
// — which is what it is.
var cacheDirSuffixes = []string{"cache"}

// isCacheDirName reports whether a folder name is regenerated cache data
// rather than save data. Matching is case- and separator-insensitive on the
// bare folder name (spaces, underscores and hyphens are ignored, so
// "Shader Cache", "shader_cache" and "ShaderCache" all match).
func isCacheDirName(name string) bool {
	n := normalizeDirName(name)
	if n == "" {
		return false
	}
	if cacheDirNames[n] {
		return true
	}
	for _, suf := range cacheDirSuffixes {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}

// normalizeDirName lowercases a folder name and strips the separators people
// use interchangeably, so one entry covers every spelling.
func normalizeDirName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch r {
		case ' ', '_', '-', '.':
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 32
		}
		b.WriteRune(r)
	}
	return b.String()
}
