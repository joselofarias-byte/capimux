package dashboard

import (
	"fmt"
	"slices"
	"strings"

	json "encoding/json/v2"
)

// backupSections are the top-level keys of a backup payload that a partial
// export/import can select, with the statements that clear each one before a
// partial restore. Inspired by 9Router Pro's selective export/import (MIT,
// © decolua and contributors); reimplemented, no code copied.
var backupSections = map[string][]string{
	"settings":            {`DELETE FROM settings`},
	"providerConnections": {`DELETE FROM providerConnections`},
	"providerNodes":       {`DELETE FROM providerNodes`},
	"proxyPools":          {`DELETE FROM proxyPools`},
	"apiKeys":             {`DELETE FROM apiKeys`},
	"combos":              {`DELETE FROM combos`},
	"modelAliases":        {`DELETE FROM kv WHERE scope = 'modelAliases'`},
	"customModels":        {`DELETE FROM kv WHERE scope = 'customModels'`},
	"mitmAlias":           {`DELETE FROM kv WHERE scope = 'mitmAlias'`},
	"pricing":             {`DELETE FROM kv WHERE scope = 'pricing'`},
}

// partialSectionsKey marks a payload as partial: only the listed sections are
// cleared and restored; everything else is left untouched.
const partialSectionsKey = "sections"

// parseBackupSections reads a comma-separated section list. An empty value
// means "full backup" (nil). Unknown names are an error.
func parseBackupSections(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := backupSections[s]; !ok {
			return nil, fmt.Errorf("sección desconocida %q; válidas: %s", s, strings.Join(sortedBackupSections(), ", "))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	slices.Sort(out)
	return out, nil
}

func sortedBackupSections() []string {
	out := make([]string, 0, len(backupSections))
	for k := range backupSections {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// filterBackupPayload keeps only the selected sections of a full export and
// records them under partialSectionsKey.
func filterBackupPayload(full *databaseExport, sections []string) (map[string]any, error) {
	b, err := json.Marshal(full)
	if err != nil {
		return nil, err
	}
	var all map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	out := map[string]any{partialSectionsKey: sections}
	for _, s := range sections {
		if v, ok := all[s]; ok {
			out[s] = v
		}
	}
	return out, nil
}

// partialImportSections returns the sections of a partial payload (nil for a
// full backup) and drops any key outside them, so a crafted partial file
// cannot write a section it does not clear.
func partialImportSections(payload map[string]any) ([]string, error) {
	raw, present := payload[partialSectionsKey]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("respaldo parcial inválido: %q debe ser una lista no vacía", partialSectionsKey)
	}
	names := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("respaldo parcial inválido: sección no textual")
		}
		names = append(names, s)
	}
	sections, err := parseBackupSections(strings.Join(names, ","))
	if err != nil {
		return nil, err
	}
	for k := range payload {
		if k != partialSectionsKey && !slices.Contains(sections, k) {
			delete(payload, k)
		}
	}
	delete(payload, partialSectionsKey)
	return sections, nil
}
