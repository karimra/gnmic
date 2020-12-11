package config

import (
	"fmt"
	"sort"
)

type OutputSuggestion struct {
	Name  string
	Types []string
}

func (c *Config) GetOutputs() (map[string]map[string]interface{}, error) {
	if len(c.Outputs) == 0 && !c.SubscribeQuiet {
		c.Outputs = make(map[string]map[string]interface{})
		c.Outputs["default-stdout"] = map[string]interface{}{
			"type":      "file",
			"file-type": "stdout",
			"format":    c.Format,
		}
	}
	// subscribe named output
	if len(c.SubscribeOutput) == 0 {
		return c.Outputs, nil
	}
	filteredOutputs := make(map[string]map[string]interface{})
	notFound := make([]string, 0)
	for _, name := range c.SubscribeOutput {
		if o, ok := c.Outputs[name]; ok {
			filteredOutputs[name] = o
		} else {
			notFound = append(notFound, name)
		}
	}
	if len(notFound) > 0 {
		return nil, fmt.Errorf("named output(s) not found in config file: %v", notFound)
	}
	if c.Debug {
		c.logger.Printf("outputs: %+v", filteredOutputs)
	}
	return filteredOutputs, nil
}

// func convert(i interface{}) interface{} {
// 	switch x := i.(type) {
// 	case map[interface{}]interface{}:
// 		nm := map[string]interface{}{}
// 		for k, v := range x {
// 			nm[k.(string)] = convert(v)
// 		}
// 		return nm
// 	case []interface{}:
// 		for i, v := range x {
// 			x[i] = convert(v)
// 		}
// 	}
// 	return i
// }

func (c *Config) GetOutputSuggestions() []OutputSuggestion {
	suggestions := make([]OutputSuggestion, 0, len(c.Outputs))
	for name, d := range c.Outputs {
		sug := OutputSuggestion{Name: name, Types: make([]string, 0)}
		if outType, ok := d["type"]; ok {
			sug.Types = append(sug.Types, outType.(string))
		}
		suggestions = append(suggestions, sug)
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Name < suggestions[j].Name
	})
	return suggestions
}
