package providers

import (
	"sort"
	"strings"
)

// models.dev raw shapes read by the catalog sync. Each model declares
// `modalities: { input: ["text","image",…], output: […] }`; an earlier
// revision used a boolean `modality` map, which no longer exists, so reading
// that key silently produced an all-false catalog. `experimental` is an
// object of service modes in the current feed, not a stability flag, so it is
// not read.
type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	Status     string `json:"status"`
	ToolCall   bool   `json:"tool_call"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit *struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost *struct {
		Input  *float64 `json:"input"`
		Output *float64 `json:"output"`
	} `json:"cost"`
}

// catalogCandidate is one upstream id competing for a provider's base key.
type catalogCandidate struct {
	id   string
	m    modelsDevModel
	mods SyncedModelModalities
}

func (c catalogCandidate) context() int {
	if c.m.Limit == nil {
		return 0
	}
	return c.m.Limit.Context
}

func (c catalogCandidate) output() int {
	if c.m.Limit == nil {
		return 0
	}
	return c.m.Limit.Output
}

// available: models.dev marks retired ids "deprecated"; anything else
// (GA, beta, alpha) can still be called.
func (c catalogCandidate) available() bool { return c.m.Status != "deprecated" }

// stability: GA (no status) > beta > alpha. Deprecated is handled by
// available(), which ranks first.
func (c catalogCandidate) stability() int {
	switch c.m.Status {
	case "", "deprecated":
		return 2
	case "beta":
		return 1
	default: // alpha and any future pre-release label
		return 0
	}
}

func (c catalogCandidate) inputCount() int {
	n := 0
	for _, b := range []bool{c.mods.Vision, c.mods.PDF, c.mods.AudioInput, c.mods.VideoInput} {
		if b {
			n++
		}
	}
	return n
}

// cost returns input+output price and whether both are declared. An unknown
// price ranks after any known one: it is not "free".
func (c catalogCandidate) cost() (float64, bool) {
	if c.m.Cost == nil || c.m.Cost.Input == nil || c.m.Cost.Output == nil {
		return 0, false
	}
	return *c.m.Cost.Input + *c.m.Cost.Output, true
}

// betterCatalogCandidate is the owner's rule for ids that collapse to the
// same provider/base key (cycle 9, decision 1), applied in this order:
//
//  1. really available (not deprecated) beats deprecated;
//  2. larger verified context window (a declared limit; 0 means unknown);
//  3. capability: larger max output, then more input modalities, then tool calling;
//  4. stability: GA > beta > alpha;
//  5. cost: lower input+output price; an undeclared price ranks last;
//  6. the upstream id, lexicographically, so the result never depends on map order.
//
// Being a ":free" variant is not a criterion by itself: it only wins when it
// ties on everything before cost.
func betterCatalogCandidate(a, b catalogCandidate) bool {
	if a.available() != b.available() {
		return a.available()
	}
	if a.context() != b.context() {
		return a.context() > b.context()
	}
	if a.output() != b.output() {
		return a.output() > b.output()
	}
	if a.inputCount() != b.inputCount() {
		return a.inputCount() > b.inputCount()
	}
	if a.m.ToolCall != b.m.ToolCall {
		return a.m.ToolCall
	}
	if a.stability() != b.stability() {
		return a.stability() > b.stability()
	}
	ac, aok := a.cost()
	bc, bok := b.cost()
	if aok != bok {
		return aok
	}
	if aok && ac != bc {
		return ac < bc
	}
	return a.id < b.id
}

func declaredModalities(inputs []string) SyncedModelModalities {
	var declared SyncedModelModalities
	for _, input := range inputs {
		switch input {
		case "image":
			declared.Vision = true
		case "pdf":
			declared.PDF = true
		case "audio":
			declared.AudioInput = true
		case "video":
			declared.VideoInput = true
		}
	}
	return declared
}

func (c catalogCandidate) variant() SyncedModelVariant {
	return SyncedModelVariant{ContextWindow: c.context(), MaxOutput: c.output(), Modalities: c.mods}
}

// buildSyncedCatalog turns the models.dev feed into the three lookup maps.
// For every provider/base key it keeps one whole variant (limits and
// modalities from the same upstream id, never mixed) chosen by
// betterCatalogCandidate. Every other variant whose limits or modalities
// differ keeps its own entry in the variants map under its full lowercased
// id, so "x:free" or a vendor-prefixed duplicate is answered with its own
// values instead of another variant's.
func buildSyncedCatalog(rawData map[string]modelsDevProvider) (
	modelsMap map[string]SyncedModelModalities,
	providersMap map[string]map[string]SyncedModelLimits,
	variantsMap map[string]map[string]SyncedModelVariant,
) {
	modelsMap = make(map[string]SyncedModelModalities)
	providersMap = make(map[string]map[string]SyncedModelLimits)
	variantsMap = make(map[string]map[string]SyncedModelVariant)

	for provID, provData := range rawData {
		groups := make(map[string][]catalogCandidate, len(provData.Models))
		for modelID, mData := range provData.Models {
			base := catalogBaseID(modelID)
			groups[base] = append(groups[base], catalogCandidate{id: modelID, m: mData, mods: declaredModalities(mData.Modalities.Input)})
		}
		for base, cands := range groups {
			sort.Slice(cands, func(i, j int) bool { return betterCatalogCandidate(cands[i], cands[j]) })
			chosen := cands[0]

			// Modalities, keyed provider+model: gateways disagree about the same
			// weights, and a bare model key would let a short id collide across
			// vendors ("auto", "free", "efficient" are router modes in one catalog
			// and model names in another). Same rule as upstream sync.js.
			if chosen.mods != (SyncedModelModalities{}) {
				for _, key := range catalogProviderKeys(provID) {
					modelsMap[key+":"+base] = chosen.mods
				}
			}
			// Limits belong to the gateway — each truncates differently — so they
			// stay keyed by provider + model.
			if chosen.context() > 0 || chosen.output() > 0 {
				if providersMap[provID] == nil {
					providersMap[provID] = make(map[string]SyncedModelLimits)
				}
				providersMap[provID][base] = SyncedModelLimits{ContextWindow: chosen.context(), MaxOutput: chosen.output()}
			}

			chosenID := strings.ToLower(chosen.id)
			for _, other := range cands[1:] {
				id := strings.ToLower(other.id)
				if id == chosenID || other.variant() == chosen.variant() {
					continue
				}
				if variantsMap[provID] == nil {
					variantsMap[provID] = make(map[string]SyncedModelVariant)
				}
				if _, taken := variantsMap[provID][id]; taken {
					continue // ids equal up to case: the better-ranked one stays
				}
				variantsMap[provID][id] = other.variant()
			}
		}
	}
	return modelsMap, providersMap, variantsMap
}
