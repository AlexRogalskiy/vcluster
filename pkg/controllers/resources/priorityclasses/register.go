package priorityclasses

import (
	"github.com/loft-sh/vcluster/cmd/vcluster/context"
	"k8s.io/client-go/tools/record"
)

func RegisterIndices(ctx *context.ControllerContext) error {
	return RegisterSyncerIndices(ctx)
}

func Register(ctx *context.ControllerContext, eventBroadcaster record.EventBroadcaster) error {
	return RegisterSyncer(ctx)
}
