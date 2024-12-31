package dashboardmetrics

import (
	"github.com/iotaledger/hive.go/app"
)

// ParametersNode contains the definition of the parameters used by the node.
type ParametersNode struct {
	// Alias is used to set an alias to identify a node
	Alias string `default:"IOTA-Core node" usage:"set an alias to identify a node"`
}

var ParamsNode = &ParametersNode{}

var params = &app.ComponentParams{
	Params: map[string]any{
		"node": ParamsNode,
	},
	Masked: nil,
}
