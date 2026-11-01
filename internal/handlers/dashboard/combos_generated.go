package dashboard

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"slices"
	"sort"
	"strings"

	"9router/proxy/internal/models"
)

// generatedCombosScope is the kv scope where the generators (auto-family and
// auto-free) record exactly what they wrote, keyed by combo id. A combo is
// "generated" only while its row still matches that record: once the user
// edits its name, models or strategy, or when the combo predates the marker,
// it counts as the user's own and is never deleted automatically.
const generatedCombosScope = "generatedCombos"

type generatedComboMarker struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Models   []string `json:"models"`
	Strategy string   `json:"strategy"`
}

func (h *DashboardHandler) markGenerated(id, kind, name string, members []string, strategy string) error {
	b, err := json.Marshal(generatedComboMarker{Kind: kind, Name: name, Models: members, Strategy: strategy})
	if err != nil {
		return err
	}
	return h.Repo.SetKV(generatedCombosScope, id, string(b))
}

func (h *DashboardHandler) generatedMarkers() (map[string]generatedComboMarker, error) {
	kv, err := h.Repo.GetKVScope(generatedCombosScope)
	if err != nil {
		return nil, err
	}
	out := make(map[string]generatedComboMarker, len(kv))
	for id, raw := range kv {
		var m generatedComboMarker
		if json.Unmarshal([]byte(raw), &m) == nil {
			out[id] = m
		}
	}
	return out, nil
}

func isGeneratorKind(kind string) bool {
	return kind == autoFamilyComboKind || kind == autoFreeComboKind
}

// isUntouchedGenerated reports whether c is still exactly what a generator
// wrote. Anything else (edited, or created before markers existed) is custom.
func isUntouchedGenerated(c *models.Combo, markers map[string]generatedComboMarker) bool {
	if c == nil || c.Kind == nil || !isGeneratorKind(*c.Kind) {
		return false
	}
	m, ok := markers[c.ID]
	if !ok || m.Kind != *c.Kind || m.Name != c.Name || m.Strategy != c.Strategy {
		return false
	}
	members, err := comboModels(c.Models)
	if err != nil {
		return false
	}
	return slices.Equal(members, m.Models)
}

// pruneGeneratedCombos deletes every untouched generated combo left with
// fewer than two valid, available models (owner decision 3, cycle 9). An
// auto-family combo counts members still among the usable chat models; the
// auto-free combo counts members still among the usable free-tier models.
// Custom combos are never touched here; GET /api/combos flags them instead.
func (h *DashboardHandler) pruneGeneratedCombos(ctx context.Context) ([]string, error) {
	markers, err := h.generatedMarkers()
	if err != nil {
		return nil, fmt.Errorf("pruneGeneratedCombos: markers: %w", err)
	}
	combos, err := h.Repo.GetCombos()
	if err != nil {
		return nil, fmt.Errorf("pruneGeneratedCombos: list combos: %w", err)
	}
	families, err := h.usableModelFamilies(ctx)
	if err != nil {
		return nil, err
	}
	usable := map[string]struct{}{}
	for _, members := range families {
		for _, m := range members {
			usable[m] = struct{}{}
		}
	}
	freeList, err := h.freeTierComboModels(ctx)
	if err != nil {
		return nil, err
	}
	free := make(map[string]struct{}, len(freeList))
	for _, m := range freeList {
		free[m] = struct{}{}
	}

	var pruned []string
	for _, c := range combos {
		if !isUntouchedGenerated(c, markers) {
			continue
		}
		valid := usable
		if *c.Kind == autoFreeComboKind {
			valid = free
		}
		members, _ := comboModels(c.Models)
		n := 0
		for _, m := range members {
			if _, ok := valid[m]; ok {
				n++
			}
		}
		if n >= minFamilyMembers {
			continue
		}
		if err := h.Repo.DeleteCombo(c.ID); err != nil {
			return pruned, fmt.Errorf("pruneGeneratedCombos: delete %s: %w", c.ID, err)
		}
		_ = h.Repo.DeleteKV(generatedCombosScope, c.ID)
		pruned = append(pruned, c.ID)
	}
	sort.Strings(pruned)
	if pruned == nil {
		pruned = []string{}
	}
	return pruned, nil
}

// comboReview is the read-only review state GET /api/combos adds to each
// combo: whether a generator still owns it, and whether it is left with fewer
// than two valid, available models.
type comboReview struct {
	Generated         bool     `json:"generated"`
	NeedsReview       bool     `json:"needsReview"`
	ReviewReason      string   `json:"reviewReason,omitempty"`
	UnavailableModels []string `json:"unavailableModels"`
}

// comboReviews computes the review state of every combo. A member is valid
// when it names another existing combo, or when it is "alias/model" whose
// provider has an active connection and whose model is not disabled. The
// check is lenient on purpose (a model missing from the bundled catalog may be
// a custom or newer one), so it only flags what is clearly unreachable.
func (h *DashboardHandler) comboReviews(combos []*models.Combo) (map[string]comboReview, error) {
	markers, err := h.generatedMarkers()
	if err != nil {
		return nil, err
	}
	conns, err := h.Repo.GetProviderConnections("", true)
	if err != nil {
		return nil, err
	}
	active := make(map[string]struct{}, len(conns))
	for _, c := range conns {
		if c.Provider != "" {
			active[c.Provider] = struct{}{}
		}
	}
	disabled, err := h.disabledModelsByAlias()
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, 2*len(combos))
	for _, c := range combos {
		names[c.Name] = struct{}{}
		names[c.ID] = struct{}{}
	}

	out := make(map[string]comboReview, len(combos))
	for _, c := range combos {
		rv := comboReview{Generated: isUntouchedGenerated(c, markers), UnavailableModels: []string{}}
		members, err := comboModels(c.Models)
		if err != nil {
			rv.NeedsReview = true
			rv.ReviewReason = "La lista de modelos del combo no es válida."
			out[c.ID] = rv
			continue
		}
		valid := 0
		for _, m := range members {
			if comboMemberAvailable(m, names, active, disabled) {
				valid++
			} else {
				rv.UnavailableModels = append(rv.UnavailableModels, m)
			}
		}
		if valid < minFamilyMembers {
			rv.NeedsReview = true
			if rv.Generated {
				rv.ReviewReason = fmt.Sprintf("Combo generado con %d modelo(s) válido(s) y disponible(s); se elimina en la próxima regeneración.", valid)
			} else {
				rv.ReviewReason = fmt.Sprintf("Combo personalizado con %d modelo(s) válido(s) y disponible(s) (mínimo 2). No se borra automáticamente: revisalo.", valid)
			}
		}
		out[c.ID] = rv
	}
	return out, nil
}

func comboMemberAvailable(member string, comboNames map[string]struct{}, active map[string]struct{}, disabled map[string]map[string]struct{}) bool {
	if _, ok := comboNames[member]; ok {
		return true
	}
	slash := strings.Index(member, "/")
	if slash <= 0 || slash == len(member)-1 {
		return false
	}
	alias, model := member[:slash], member[slash+1:]
	if !activeProviderAlias(active, alias) {
		return false
	}
	_, off := disabled[alias][model]
	return !off
}

// comboWithReview renders a combo with its review fields added, keeping
// every existing field as it was.
func comboWithReview(c *models.Combo, rv comboReview) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["generated"] = rv.Generated
	m["needsReview"] = rv.NeedsReview
	if rv.ReviewReason != "" {
		m["reviewReason"] = rv.ReviewReason
	}
	m["unavailableModels"] = rv.UnavailableModels
	return m, nil
}
