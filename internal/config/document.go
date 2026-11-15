package config

import (
	"bytes"
	"context"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
	"server-man/internal/yamlutil"
)

// UnmarshalYAML applies defaults before decoding the optional server section.
// A separate decoder retains strict field checking inside this custom type.
func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	defaults, err := Defaults()
	if err != nil {
		return err
	}
	if node.Kind != yaml.MappingNode {
		return errors.New("server settings must be a mapping")
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return errors.New("invalid server settings")
	}
	type plain Config
	decoded := plain(defaults)
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if decoder.Decode(&decoded) != nil {
		return errors.New("server settings contain an unknown field or incorrect field type")
	}
	*c = Config(decoded)
	return c.Validate()
}

// ReadServer validates the complete document structure while leaving typed
// workload validation to the management layer, so invalid resources are repairable.
func ReadServer(data []byte) (*Config, error) {
	if len(data) > 1<<20 {
		return nil, errors.New("configuration exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if decoder.Decode(&node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("configuration must be one YAML mapping")
	}
	if err := yamlutil.CheckStructure(context.Background(), &node, 128, 1<<20); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("configuration must contain one YAML document")
	}
	var document struct {
		Version  int       `yaml:"version"`
		Server   *Config   `yaml:"server"`
		Services yaml.Node `yaml:"services"`
		Storage  yaml.Node `yaml:"storage"`
		Shares   yaml.Node `yaml:"shares"`
		Bindings yaml.Node `yaml:"bindings"`
		Secrets  yaml.Node `yaml:"secrets"`
	}
	typed := yaml.NewDecoder(bytes.NewReader(data))
	typed.KnownFields(true)
	if err := typed.Decode(&document); err != nil {
		return nil, errors.New("configuration contains invalid server settings, an unknown field or incorrect field type")
	}
	if document.Version != 1 {
		return nil, errors.New("unsupported configuration version; expected 1")
	}
	return document.Server, nil
}
