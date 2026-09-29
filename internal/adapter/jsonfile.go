package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeObject is the JSON object in content, the file at path, keeping its
// numbers as they were written; it is empty when content is.
func decodeObject(path, content string) (map[string]any, error) {
	obj := map[string]any{}
	if strings.TrimSpace(content) == "" {
		return obj, nil
	}
	d := json.NewDecoder(strings.NewReader(content))
	d.UseNumber() // keep the person's numbers as they wrote them
	if err := d.Decode(&obj); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

// encodeObject is obj as indented JSON, or empty when obj has no keys.
func encodeObject(obj map[string]any) (string, error) {
	if len(obj) == 0 {
		return "", nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return "", err
	}
	return b.String(), nil
}
