package importmapping

import (
	"errors"
	"strings"
)

const MaxColumns = 200

var ErrInvalid = errors.New("invalid column mapping")

type Mapping map[string]int

type Field struct {
	Key      string
	Name     string
	Required bool
	Custom   bool
	Aliases  []string
}

type Column struct {
	Index    int      `json:"index"`
	Name     string   `json:"name"`
	Examples []string `json:"examples"`
}

type FieldOption struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	Required        bool   `json:"required"`
	Custom          bool   `json:"custom"`
	SuggestedColumn *int   `json:"suggested_column"`
}

type Analysis struct {
	Columns []Column      `json:"columns"`
	Fields  []FieldOption `json:"fields"`
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "\ufeff")))
}

func suggestedIndexes(headers []string, fields []Field) map[string]int {
	result := make(map[string]int, len(fields))
	used := make(map[int]struct{}, len(fields))
	for _, field := range fields {
		result[field.Key] = -1
		aliases := make(map[string]struct{}, len(field.Aliases)+1)
		aliases[normalize(field.Key)] = struct{}{}
		for _, alias := range field.Aliases {
			aliases[normalize(alias)] = struct{}{}
		}
		for index, header := range headers {
			if _, exists := used[index]; exists {
				continue
			}
			if _, matches := aliases[normalize(header)]; matches {
				result[field.Key] = index
				used[index] = struct{}{}
				break
			}
		}
	}
	return result
}

// Analyze returns source columns, sample values and automatic mapping suggestions.
func Analyze(rows [][]string, fields []Field) (Analysis, error) {
	if len(rows) == 0 || len(rows[0]) == 0 || len(rows[0]) > MaxColumns {
		return Analysis{}, ErrInvalid
	}
	headers := rows[0]
	suggestions := suggestedIndexes(headers, fields)
	analysis := Analysis{
		Columns: make([]Column, 0, len(headers)),
		Fields:  make([]FieldOption, 0, len(fields)),
	}
	for index, header := range headers {
		column := Column{Index: index, Name: strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")), Examples: []string{}}
		seen := map[string]struct{}{}
		for rowIndex := 1; rowIndex < len(rows) && len(column.Examples) < 3; rowIndex++ {
			if index >= len(rows[rowIndex]) {
				continue
			}
			value := strings.TrimSpace(rows[rowIndex][index])
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			column.Examples = append(column.Examples, value)
		}
		analysis.Columns = append(analysis.Columns, column)
	}
	for _, field := range fields {
		option := FieldOption{Key: field.Key, Name: field.Name, Required: field.Required, Custom: field.Custom}
		if index := suggestions[field.Key]; index >= 0 {
			value := index
			option.SuggestedColumn = &value
		}
		analysis.Fields = append(analysis.Fields, option)
	}
	return analysis, nil
}

// Resolve validates an optional explicit mapping and returns field indexes plus
// normalized headers for the existing custom-field spreadsheet parser.
func Resolve(headers []string, mapping Mapping, fields []Field) (map[string]int, []string, error) {
	if len(headers) == 0 || len(headers) > MaxColumns {
		return nil, nil, ErrInvalid
	}
	byKey := make(map[string]Field, len(fields))
	indexes := make(map[string]int, len(fields))
	for _, field := range fields {
		if field.Key == "" {
			return nil, nil, ErrInvalid
		}
		byKey[field.Key] = field
		indexes[field.Key] = -1
	}

	if len(mapping) == 0 {
		indexes = suggestedIndexes(headers, fields)
	} else {
		used := make(map[int]struct{}, len(mapping))
		for key, index := range mapping {
			if _, allowed := byKey[key]; !allowed || index < 0 || index >= len(headers) {
				return nil, nil, ErrInvalid
			}
			if _, duplicate := used[index]; duplicate {
				return nil, nil, ErrInvalid
			}
			used[index] = struct{}{}
			indexes[key] = index
		}
	}

	for _, field := range fields {
		if field.Required && indexes[field.Key] < 0 {
			return nil, nil, ErrInvalid
		}
	}

	normalizedHeaders := append([]string(nil), headers...)
	if len(mapping) > 0 {
		for index, header := range normalizedHeaders {
			if strings.HasPrefix(normalize(header), "custom:") {
				normalizedHeaders[index] = ""
			}
		}
	}
	for _, field := range fields {
		if !field.Custom {
			continue
		}
		if index := indexes[field.Key]; index >= 0 {
			normalizedHeaders[index] = field.Key
		}
	}
	return indexes, normalizedHeaders, nil
}
