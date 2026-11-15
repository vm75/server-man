package declarative

import (
	"context"
	"go.yaml.in/yaml/v3"
	"server-man/internal/yamlutil"
)

func checkYAMLStructure(ctx context.Context, node *yaml.Node, maxDepth, maxNodes int) error {
	return yamlutil.CheckStructure(ctx, node, maxDepth, maxNodes)
}
