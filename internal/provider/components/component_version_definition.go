package components

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var errInvalidDefinition = errors.New("component version definition must be a single JSON object")

func normalizeDefinition(definition string) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errInvalidDefinition
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errInvalidDefinition
	}

	root, ok := value.(map[string]any)
	if !ok {
		return nil, errInvalidDefinition
	}

	if phases, ok := root["phases"].([]any); ok {
		for _, phaseValue := range phases {
			phase, ok := phaseValue.(map[string]any)
			if !ok {
				continue
			}
			steps, ok := phase["steps"].([]any)
			if !ok {
				continue
			}
			for _, stepValue := range steps {
				step, ok := stepValue.(map[string]any)
				if !ok {
					continue
				}
				if _, ok := step["timeoutSeconds"]; !ok {
					step["timeoutSeconds"] = 7200
				}
				if _, ok := step["onFailure"]; !ok {
					step["onFailure"] = "Abort"
				}
				if _, ok := step["maxAttempts"]; !ok {
					step["maxAttempts"] = 1
				}
			}
		}
	}

	canonical, err := json.Marshal(root)
	if err != nil {
		return nil, errInvalidDefinition
	}
	return canonical, nil
}
