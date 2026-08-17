package httpserver

import (
	"encoding/json"
	"strings"
)

func parseImportColumnMapping(raw string) (map[string]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var mapping map[string]int
	if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
		return nil, err
	}
	return mapping, nil
}
