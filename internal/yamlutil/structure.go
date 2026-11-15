package yamlutil

import (
	"context"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// One structural policy for authoritative configuration and private native
// metadata: scalar string keys, no aliases/custom tags and bounded work.
func CheckStructure(ctx context.Context, node *yaml.Node, maxDepth, maxNodes int) error {
	count := 0
	var visit func(*yaml.Node, int) error
	visit = func(n *yaml.Node, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if depth > maxDepth || count > maxNodes {
			return errors.New("YAML structure exceeds its limit")
		}
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 {
			return fmt.Errorf("unsupported YAML construct at line %d", n.Line)
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
					return fmt.Errorf("invalid or duplicate mapping key at line %d", key.Line)
				}
				seen[key.Value] = true
			}
		}
		for _, child := range n.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(node, 0)
}
