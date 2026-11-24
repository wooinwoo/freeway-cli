// SPDX-License-Identifier: MIT

package router

import "strings"

var modelVendors = map[string]string{"z-ai": "zai", "meta-llama": "meta", "particle": "particle-ai"}

var bareModelVendors = []struct{ prefix, vendor string }{
	{"deepseek-", "deepseek"}, {"nemotron-", "nvidia"}, {"glm-", "zai"},
	{"minimax-", "minimax"}, {"gpt-oss-", "openai"}, {"gemini-", "google"},
}

func modelIdentity(id, upstreamID string) string {
	// These OpenCode catalog aliases share the existing Kilo model IDs.
	// Preserve those IDs so saved conversations continue to select the same model.
	switch id {
	case "opencode/beam-free":
		return "model/reflection/beam"
	case "opencode/flint-40b-free":
		return "model/recursive/flint-v1.0"
	}
	// Free-tier markers identify an offering, while versions and variants identify the model.
	slug := strings.ToLower(upstreamID)
	if freeSlug, ok := strings.CutSuffix(slug, ":free"); ok {
		slug = freeSlug
	} else {
		slug = strings.TrimSuffix(slug, "-free")
	}
	if vendor, name, namespaced := strings.Cut(slug, "/"); namespaced {
		if vendor == "" || vendor == "kilo-auto" || vendor == "openrouter" || name == "" {
			return id
		}
		if alias := modelVendors[vendor]; alias != "" {
			vendor = alias
		}
		return "model/" + vendor + "/" + name
	}
	// Bare aliases must have a known manufacturer so the ID survives catalog outages.
	for _, known := range bareModelVendors {
		if strings.HasPrefix(slug, known.prefix) && len(slug) > len(known.prefix) {
			return "model/" + known.vendor + "/" + slug
		}
	}
	return id
}

func modelName(candidate route) string {
	name := candidate.model.Name
	if name == "" {
		name = candidate.model.UpstreamID
	}
	if candidate.groupID == candidate.model.ID {
		return name
	}
	if _, rest, ok := strings.Cut(name, ": "); ok && rest != "" {
		name = rest
	}
	lower := strings.ToLower(name)
	for _, suffix := range []string{" (free)", " free", ":free", "-free"} {
		if strings.HasSuffix(lower, suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}
