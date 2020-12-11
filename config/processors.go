package config

func (c *Config) GetEventProcessors() (map[string]map[string]interface{}, error) {
	return c.Processors, nil
}
