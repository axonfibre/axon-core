package dashboardmetrics

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/dig"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/runtime/timeutil"
	"github.com/iotaledger/inx-app/pkg/httpserver"
	"github.com/iotaledger/iota-core/pkg/daemon"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/network/p2p"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	restapipkg "github.com/iotaledger/iota-core/pkg/restapi"
)

const (
	// RouteNodeInfoExtended is the route to get additional info about the node.
	// GET returns the extended info of the node.
	RouteNodeInfoExtended = "/info"

	// RouteDatabaseSizes is the route to get the size of the databases.
	// GET returns the sizes of the databases.
	RouteDatabaseSizes = "/database/sizes"

	// RouteGossipMetrics is the route to get metrics about gossip.
	// GET returns the gossip metrics.
	RouteGossipMetrics = "/gossip"
)

func init() {
	Component = &app.Component{
		Name:      "DashboardMetrics",
		DepsFunc:  func(cDeps dependencies) { deps = cDeps },
		Params:    params,
		Configure: configure,
		Run:       run,
	}
}

var (
	Component *app.Component
	deps      dependencies
)

type dependencies struct {
	dig.In

	Host             host.Host
	Protocol         *protocol.Protocol
	RestRouteManager *restapipkg.RestRouteManager
	AppInfo          *app.Info
	P2PMetrics       *p2p.P2PMetrics
}

func configure() error {
	// configure protocol events
	deps.Protocol.Network.OnBlockReceived(func(_ *model.Block, _ peer.ID) {
		deps.P2PMetrics.IncomingBlocks.Add(1)
	})

	deps.Protocol.Events.Engine.BlockRetainer.BlockRetained.Hook(func(block *blocks.Block) {
		deps.P2PMetrics.IncomingNewBlocks.Add(1)
	})

	// configure rest routes
	routeGroup := deps.RestRouteManager.AddRoute("dashboard-metrics/v2")

	routeGroup.GET(RouteNodeInfoExtended, func(c echo.Context) error {
		return httpserver.JSONResponse(c, http.StatusOK, nodeInfoExtended())
	})

	routeGroup.GET(RouteDatabaseSizes, func(c echo.Context) error {
		return httpserver.JSONResponse(c, http.StatusOK, databaseSizesMetrics())
	})

	routeGroup.GET(RouteGossipMetrics, func(c echo.Context) error {
		return httpserver.JSONResponse(c, http.StatusOK, gossipMetrics())
	})

	return nil
}

func run() error {
	Component.Logger.LogInfof("Starting %s ...", Component.Name)

	// create a background worker that "measures" the BPS value every second
	if err := Component.Daemon().BackgroundWorker("Metrics BPS Updater", func(ctx context.Context) {
		timeutil.NewTicker(measureGossipMetrics, 1*time.Second, ctx).WaitForGracefulShutdown()
	}, daemon.PriorityDashboardMetrics); err != nil {
		Component.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}
