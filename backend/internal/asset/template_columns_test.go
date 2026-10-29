package asset

import (
	"bytes"
	"fmt"
	"testing"

	"netassets/internal/customfield"
	"netassets/internal/importmapping"
)

func TestConfiguredTemplateOver200Columns(t *testing.T) {
	definitions := make([]customfield.Definition, 204-len(assetImportFields(nil)))
	for index := range definitions {
		definitions[index] = customfield.Definition{
			FieldKey: fmt.Sprintf("field_%d", index), FieldName: fmt.Sprintf("字段%d", index), FieldType: "text", IsEnabled: true,
		}
	}
	for _, format := range []string{"csv", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			content, _, name, err := buildTemplate(format, definitions)
			if err != nil {
				t.Fatal(err)
			}
			rows, _, err := readImportRows(name, bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			analysis, err := importmapping.Analyze(dropAssetTemplateExampleRows(rows, definitions), assetImportFields(definitions))
			if err != nil || len(analysis.Columns) != 204 {
				t.Fatalf("analyze downloaded template: columns=%d error=%v", len(analysis.Columns), err)
			}
			for _, column := range analysis.Columns {
				if len(column.Examples) != 0 {
					t.Fatalf("template sample counted as data: %#v", column)
				}
			}
			indexes, normalized, err := resolveAssetImportHeaders(rows[0], nil, definitions)
			if err != nil || len(normalized) != 204 {
				t.Fatalf("resolve downloaded template: columns=%d error=%v", len(normalized), err)
			}
			if countAssetImportDataRows(rows, indexes) != 0 {
				t.Fatal("template sample counted as an asset")
			}
		})
	}
}
